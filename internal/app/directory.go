package app

import (
	"context"
	"errors"
)

var ErrDirectoryUnavailable = errors.New("directory unavailable")

type DirectoryUser struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	Email     string `json:"email"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
}

type UserDirectory interface {
	SearchUsers(ctx context.Context, query string, first, max int) ([]DirectoryUser, error)
	GetUserByID(ctx context.Context, id string) (DirectoryUser, error)
}
