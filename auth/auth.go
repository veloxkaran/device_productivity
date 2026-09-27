package auth

import (
	"crypto/rand"
	"encoding/base32"
	"log"
	"os"
	"path/filepath"
	"strings"
	"my-monitor/storage"

	"golang.org/x/crypto/bcrypt"
)

func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(b), err
}

func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// EnsureDefaultUser creates an admin user if the users table is empty.
// Returns true if a new default user was created (first run).
func EnsureDefaultUser(db *storage.DB) bool {
	n, err := db.UserCount()
	if err != nil {
		log.Printf("auth: user count error: %v", err)
		return false
	}
	if n > 0 {
		return false
	}
	// Generate a strong random password instead of a universal default, so no
	// two installs share credentials. It is written once to a local 0600 file
	// (data/admin-credentials.txt) that whoever installs the app can read.
	pw := randomPassword()
	hash, err := HashPassword(pw)
	if err != nil {
		log.Printf("auth: hash error: %v", err)
		return false
	}
	if err := db.CreateUser("admin", hash); err != nil {
		log.Printf("auth: create default user error: %v", err)
		return false
	}
	credPath := filepath.Join("data", "admin-credentials.txt")
	body := "My Monitor local admin\nusername: admin\npassword: " + pw + "\n\nChange this in the Setup page, then delete this file.\n"
	if err := os.WriteFile(credPath, []byte(body), 0600); err != nil {
		// Fall back to logging so the password is not lost on first run.
		log.Printf("auth: could not write %s: %v (admin password: %s)", credPath, err, pw)
	} else {
		log.Printf("auth: created admin user; password saved to %s", credPath)
	}
	return true
}

// randomPassword returns a 16-char base32 (unambiguous, no padding) secret.
func randomPassword() string {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		// Extremely unlikely; fall back to a timestamp-seeded value.
		return "chg-" + strings.ToLower(base32.StdEncoding.EncodeToString([]byte("fallbackseed!")))[:12]
	}
	return strings.ToLower(strings.TrimRight(base32.StdEncoding.EncodeToString(b), "="))
}
