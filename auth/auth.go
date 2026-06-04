package auth

import (
	"log"
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
	hash, err := HashPassword("admin")
	if err != nil {
		log.Printf("auth: hash error: %v", err)
		return false
	}
	if err := db.CreateUser("admin", hash); err != nil {
		log.Printf("auth: create default user error: %v", err)
		return false
	}
	return true
}
