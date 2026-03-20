package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
)

// Encrypt data using AES-GCM
func encrypt(data []byte, passphrase string) ([]byte, error) {
	block, err := aes.NewCipher([]byte(passphrase))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	ciphertext := gcm.Seal(nonce, nonce, data, nil)
	return ciphertext, nil
}

// Decrypt data using AES-GCM
func decrypt(data []byte, passphrase string) ([]byte, error) {
	block, err := aes.NewCipher([]byte(passphrase))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	return gcm.Open(nil, nonce, ciphertext, nil)
}

func saveEncryptedCache(cache interface{}, filename string, passphrase string) error {
	data, err := json.Marshal(cache)
	if err != nil {
		return err
	}
	encrypted, err := encrypt(data, passphrase)
	if err != nil {
		return err
	}
	return os.WriteFile(filename, encrypted, 0644)
}

func loadEncryptedCache(cache interface{}, filename string, passphrase string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	decrypted, err := decrypt(data, passphrase)
	if err != nil {
		return err
	}
	return json.Unmarshal(decrypted, cache)
}
