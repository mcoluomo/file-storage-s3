package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
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
		respondWithError(w, http.StatusUnauthorized, "wrong id was given", nil)
		return
	}
	log.Println("uploading thumbnail for video:", videoID, "by user:", userID)

	// TODO: implement the upload here
	const maxMemory int64 = 10 << 20
	if err = r.ParseMultipartForm(maxMemory); err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse Multipart form", err)
		return
	}

	file, _, err := r.FormFile("thumbnail")
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

	if int64(len(fileBytes)) > maxMemory {
		respondWithError(w, http.StatusBadRequest, "File size exceeds the limit of 10MB", nil)
		return
	}

	contentType := http.DetectContentType(fileBytes)
	allowedTypes := map[string]bool{
		"image/jpeg": true,
		"image/png":  true,
		"image/gif":  true,
	}
	if !allowedTypes[contentType] {
		respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Unsupported file type: %s", contentType), nil)
		return
	}

	fileExt, err := mime.ExtensionsByType(contentType)
	if err != nil || len(fileExt) == 0 {
		respondWithError(w, http.StatusInternalServerError, "Could not determine file extension", err)
		return
	}

	key := make([]byte, 32)
	rand.Read(key)
	URLEncoding := base64.RawURLEncoding.EncodeToString(key)
	fileName := URLEncoding + fileExt[0]
	assetsFilePath := filepath.Join(cfg.assetsRoot, fileName)
	thumbnailURL := fmt.Sprintf("http://localhost:%s/assets/%s", cfg.port, fileName)

	// Write the validated file data to disk in one step
	if err = os.WriteFile(assetsFilePath, fileBytes, 0o644); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to save file to disk", err)
		return
	}

	video.ThumbnailURL = &thumbnailURL
	if err = cfg.db.UpdateVideo(video); err != nil {
		respondWithError(w, http.StatusBadRequest, "failed updating video metadata", err)
		return
	}

	log.Printf("Successfully saved thumbnail for video %s at %s", video.ID, assetsFilePath)
	respondWithJSON(w, http.StatusOK, video)
}
