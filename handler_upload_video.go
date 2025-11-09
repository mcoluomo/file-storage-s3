package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

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

	log.Printf("written bytes: %d", writtenBytes)
	if _, err = tempFile.Seek(0, io.SeekStart); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to rewind file", err)
		return
	}
	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video", err)
		return
	}
	hexKey, err := generateRandomKey()
	if err != nil {
		panic(err)
	}

	key := hexKey + fileExt[3]
	log.Print(key)

	putObjectOutput, err := cfg.s3Client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &key,
		Body:        tempFile,
		ContentType: &contentType,
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to upload video to S3", err)
		return
	}

	log.Printf("Successfully uploaded video. ETag: %s", *putObjectOutput.ETag)

	videoURL := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", cfg.s3Bucket, cfg.s3Region, key)

	video.VideoURL = &videoURL
	if err = cfg.db.UpdateVideo(video); err != nil {
		respondWithError(w, http.StatusBadRequest, "failed updating video metadata", err)
		return
	}

	log.Printf("Successfully uploaded video %s to Bucket %s", video.ID, cfg.s3Bucket)
	respondWithJSON(w, http.StatusOK, video)
}
