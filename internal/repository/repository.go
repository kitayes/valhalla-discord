package repository

import (
	"database/sql"
	"time"
	"valhalla/internal/models"
)

type Match interface {
	Create(match models.Match) (int, error)
	Exists(fileHash, matchSignature string) (bool, error)
	GetAllAfter(date time.Time) ([]models.Match, error)

	SetSeasonStartDate(date time.Time) error
	GetSeasonStartDate() (time.Time, error)

	Delete(id int) error
	WipeAll() error

	SetPlayerResetDate(playerName string, date time.Time) error
	GetPlayerResetDates() (map[string]time.Time, error)
}

type Repository struct {
	Match
	db *sql.DB
}

func NewRepository(cfg *Config, db *sql.DB) *Repository {
	return &Repository{
		Match: NewMatchPostgres(db),
		db:    db,
	}
}
