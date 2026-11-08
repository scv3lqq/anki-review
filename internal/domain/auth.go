package domain

import "time"

// AuthSession stores an authenticated session digest without retaining its bearer token.
type AuthSession struct {
	Hash    string    `json:"hash"`
	User    string    `json:"user"`
	Expires time.Time `json:"expires"`
}
