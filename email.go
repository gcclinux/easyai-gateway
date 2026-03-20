package main

import (
	"bytes"
	"crypto/rand"
	"html/template"
	"math/big"
	"net/smtp"
	"os"
	"sync"
	"time"
)

const tokenCharset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"

func generateToken() (string, error) {
	b := make([]byte, 64)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(tokenCharset))))
		if err != nil {
			return "", err
		}
		b[i] = tokenCharset[n.Int64()]
	}
	return string(b), nil
}

func sendLoginToken(email, token string) error {
	gmailUser := os.Getenv("GMAIL_USER")
	gmailPass := os.Getenv("GMAIL_PASS")

	if gmailUser == "" || gmailPass == "" {
		return nil // skip if not configured, though you'd typically log this
	}

	smtpHost := "smtp.gmail.com"
	smtpPort := "587"
	auth := smtp.PlainAuth("", gmailUser, gmailPass, smtpHost)

	// Parse the email template
	tmpl, err := template.ParseFiles("templates/email.html")
	if err != nil {
		return err
	}

	// Execute the template with the token
	var body bytes.Buffer
	err = tmpl.Execute(&body, struct{ Token string }{Token: token})
	if err != nil {
		return err
	}

	to := []string{email}

	// Construct the MIME headers for an HTML email
	mime := "MIME-version: 1.0;\nContent-Type: text/html; charset=\"UTF-8\";\n\n"
	subject := "Subject: Your Admin Login Token\n"
	header := "To: " + email + "\n" + subject + mime

	msg := []byte(header + body.String())

	return smtp.SendMail(smtpHost+":"+smtpPort, auth, gmailUser, to, msg)
}

func sendNewClientEmail(email, licenseId, credits, application string) error {
	gmailUser := os.Getenv("GMAIL_USER")
	gmailPass := os.Getenv("GMAIL_PASS")

	if gmailUser == "" || gmailPass == "" {
		return nil 
	}

	smtpHost := "smtp.gmail.com"
	smtpPort := "587"
	auth := smtp.PlainAuth("", gmailUser, gmailPass, smtpHost)

	// Parse the email template
	tmpl, err := template.ParseFiles("templates/new-client-email.html")
	if err != nil {
		return err
	}

	// Execute the template with the user details
	data := struct {
		LicenseID   string
		Email       string
		Credits     string
		Application string
	}{
		LicenseID:   licenseId,
		Email:       email,
		Credits:     credits,
		Application: application,
	}

	var body bytes.Buffer
	err = tmpl.Execute(&body, data)
	if err != nil {
		return err
	}

	to := []string{email}
	mime := "MIME-version: 1.0;\nContent-Type: text/html; charset=\"UTF-8\";\n\n"
	subject := "Subject: Welcome to EasyAI - Your License Details\n"
	header := "To: " + email + "\n" + subject + mime

	msg := []byte(header + body.String())

	return smtp.SendMail(smtpHost+":"+smtpPort, auth, gmailUser, to, msg)
}

type Token struct {
	Value      string
	Expiration time.Time
}

var (
	tokenMap = make(map[string]Token)
	tokenMu  sync.Mutex
)

func storeToken(email, token string) {
	tokenMu.Lock()
	defer tokenMu.Unlock()
	tokenMap[email] = Token{
		Value:      token,
		Expiration: time.Now().Add(10 * time.Minute),
	}
}

func isValidToken(email, token string) bool {
	tokenMu.Lock()
	defer tokenMu.Unlock()
	storedToken, ok := tokenMap[email]
	if !ok {
		return false
	}
	if storedToken.Value != token {
		return false
	}
	if time.Now().After(storedToken.Expiration) {
		delete(tokenMap, email)
		return false
	}
	return true
}

func invalidateToken(email string) {
	tokenMu.Lock()
	defer tokenMu.Unlock()
	delete(tokenMap, email)
}
