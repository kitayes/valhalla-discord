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
	Delete(id int) error
	WipeAll() error

	SetSeasonStartDate(date time.Time) error
	GetSeasonStartDate() (time.Time, error)

	SetPlayerResetDate(playerName string, date time.Time) error
	GetPlayerResetDates() (map[string]time.Time, error)

	GetHistory(playerName string, limit int) ([]models.Match, error)
	EnsurePlayerExists(name string) error
	GetAllPlayers() ([]models.Player, error)
	GetPlayerNameByID(id int) (string, error)
	WipePlayerByID(id int) error
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
