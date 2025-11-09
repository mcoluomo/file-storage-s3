package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
)

func (cfg apiConfig) ensureAssetsDir() error {
	if _, err := os.Stat(cfg.assetsRoot); os.IsNotExist(err) {
		return os.Mkdir(cfg.assetsRoot, 0o755)
	}
	return nil
}

func generateRandomKey() (string, error) {
	randomBytes := make([]byte, 32)

	// Fill the slice with cryptographically secure random bytes
	_, err := rand.Read(randomBytes)
	if err != nil {
		return "", fmt.Errorf("gernerateRandomKey failed filling slice: %w", err)
	}

	hexString := hex.EncodeToString(randomBytes)
	return hexString, nil
}
