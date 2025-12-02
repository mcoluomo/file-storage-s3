package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

const expireTime time.Duration = 900 * time.Second

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "failed getting video", err)
		return
	}

	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "wrong UserID was given", nil)
		return
	}
	log.Println("uploading thumbnail for video:", videoID, "by user:", userID)

	const maxUploadSize int64 = 1 << 30
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)

	file, _, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()

	fileBytes, err := io.ReadAll(file)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "failed reading form file", err)
		return
	}

	contentType := http.DetectContentType(fileBytes)
	allowedTypes := map[string]bool{
		"video/mp4": true,
	}
	if _, exists := allowedTypes[contentType]; !exists {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Unsupported file type: %s", contentType), nil)
		return
	}
	fileExt, err := mime.ExtensionsByType(contentType)
	if err != nil || len(fileExt) == 0 {
		respondWithError(w, http.StatusInternalServerError, "Could not determine file extension", err)
		return
	}

	tempFile, err := os.CreateTemp("", "tubely-upload-*.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to create temp file", err)
		return
	}

	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	_, err = file.Seek(0, io.SeekStart)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to rewind file", err)
		return
	}

	writtenBytes, err := io.Copy(tempFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error saving file", err)
		return
	}

	videoAspRatio, err := getVideoAspectRatio(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	log.Println("aspect ratio", videoAspRatio)

	log.Printf("written bytes: %d", writtenBytes)
	if _, err = tempFile.Seek(0, io.SeekStart); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to rewind file", err)
		return
	}

	processedFilePath, err := processVideoForFastStart(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, err.Error(), nil)
		return
	}

	processedFile, err := os.Open(processedFilePath)
	if err != nil {
		errMsg := fmt.Sprintf("failed opening file: %s", processedFilePath)
		respondWithError(w, http.StatusInternalServerError, errMsg, err)
		return
	}
	defer processedFile.Close()
	defer os.Remove(processedFilePath)

	if err = cfg.db.UpdateVideo(video); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video", err)
		return
	}
	hexKey, err := generateRandomKey()
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failded generating key", err)
		return
	}

	key := videoAspRatio + hexKey + fileExt[3]
	log.Println(key)

	putObjectOutput, err := cfg.s3Client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &key,
		Body:        processedFile,
		ContentType: &contentType,
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed ", err)
		return
	}

	log.Printf("Successfully uploaded video. ETag: %s\n", *putObjectOutput.ETag)

	videoURL := cfg.s3CfDistribution + key

	log.Println("videoURL: ", videoURL)

	video.VideoURL = &videoURL
	if err = cfg.db.UpdateVideo(video); err != nil {
		respondWithError(w, http.StatusBadRequest, "failed updating video metadata", err)
		return
	}

	log.Printf("Successfully uploaded video %s to Bucket %s\n", video.ID, cfg.s3Bucket)
	respondWithJSON(w, http.StatusOK, video)
}

func getVideoAspectRatio(filePath string) (string, error) {
	var bytesBuf bytes.Buffer
	args := []string{"-v", "error", "-print_format", "json", "-show_streams", filePath}
	var cmd *exec.Cmd = exec.Command("ffprobe", args...)
	cmd.Stdout = &bytesBuf

	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed running ffprobe command: %w", err)
	}
	var ffprobeOutput struct {
		Streams []struct {
			Width              int    `json:"width"`
			Height             int    `json:"height"`
			CodecType          string `json:"codec_type"`
			DisplayAspectRatio string `json:"display_aspect_ratio"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(bytesBuf.Bytes(), &ffprobeOutput); err != nil {
		return "", fmt.Errorf("failed unmarshelling json data: %w", err)
	}
	if len(ffprobeOutput.Streams) == 0 {
		return "", errors.New("no video streams found")
	}

	if ffprobeOutput.Streams[0].CodecType != "video" {
		return "", fmt.Errorf("ERR: codec type is not video: %s", ffprobeOutput.Streams[0].CodecType)
	}

	var ratio string

	width := ffprobeOutput.Streams[0].Width
	height := ffprobeOutput.Streams[0].Height

	if width == 16*height/9 {
		ratio = "16:9"
	} else if height == 16*width/9 {
		ratio = "9:16"
	}

	log.Println(ratio)
	switch ratio {
	case "9:16":
		return "portrait/", nil

	case "16:9":
		return "landscape/", nil
	}

	return "other/", nil
}
