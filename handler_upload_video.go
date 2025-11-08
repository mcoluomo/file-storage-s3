package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

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
	http.MaxBytesReader(w, r.Body, maxUploadSize)

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

	if int64(len(fileBytes)) > maxUploadSize {
		respondWithError(w, http.StatusBadRequest, "File size exceeds the limit of 1000MB", nil)
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

	tempFile, err := os.CreateTemp("", "tubely-upload-*.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to create temp file", err)
		return
	}

	defer tempFile.Close()
	defer os.Remove(tempFile.Name())

	// Write the bytes directly to the open file handle

	writtenBytes, err := io.Copy(tempFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error saving file", err)
		return
	}
	log.Printf("written bytes: %d", writtenBytes)

	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video", err)
		return
	}
	tempFile.Seek(0, io.SeekStart)

	cfg.s3Client.PutObject(context.TODO())

	respondWithJSON(w, http.StatusOK, video)
}
