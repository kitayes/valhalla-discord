package application

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
	"valhalla/internal/models"
	"valhalla/internal/repository"

	"github.com/xuri/excelize/v2"
)

type MatchServiceImpl struct {
	repo   repository.Match
	ai     AIProvider
	logger Logger
}

func NewMatchServiceImpl(repo repository.Match, ai AIProvider, logger Logger) *MatchServiceImpl {
	return &MatchServiceImpl{repo: repo, ai: ai, logger: logger}
}

func (s *MatchServiceImpl) ProcessImage(data []byte) error {
	fHash := sha256.Sum256(data)
	fileHashStr := hex.EncodeToString(fHash[:])

	players, err := s.ai.AnalyzeScreenshot(data)
	if err != nil {
		return fmt.Errorf("AI Error: %w", err)
	}

	sig := s.generateSignature(players)

	exists, err := s.repo.Exists(fileHashStr, sig)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("duplicate match detected")
	}

	match := models.Match{
		FileHash:       fileHashStr,
		MatchSignature: sig,
		Players:        players,
	}
	_, err = s.repo.Create(match)
	return err
}

func (s *MatchServiceImpl) generateSignature(players []models.PlayerResult) string {
	var parts []string
	for _, p := range players {
		parts = append(parts, fmt.Sprintf("%s|%d/%d/%d|%s", p.PlayerName, p.Kills, p.Deaths, p.Assists, p.Result))
	}
	sort.Strings(parts)
	raw := strings.Join(parts, ";")
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

func (s *MatchServiceImpl) SetTimer(dateStr string) error {
	date, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return fmt.Errorf("неверный формат даты, используйте YYYY-MM-DD")
	}
	return s.repo.SetSeasonStartDate(date)
}

func (s *MatchServiceImpl) ResetGlobal() error {
	return s.repo.SetSeasonStartDate(time.Now())
}

func (s *MatchServiceImpl) ResetPlayer(playerName string, dateStr string) error {
	var date time.Time
	var err error

	if dateStr == "" || dateStr == "now" {
		date = time.Now()
	} else {
		date, err = time.Parse("2006-01-02", dateStr)
		if err != nil {
			return fmt.Errorf("неверный формат даты")
		}
	}
	return s.repo.SetPlayerResetDate(playerName, date)
}

func (s *MatchServiceImpl) GetExcelReport() ([]byte, error) {
	seasonStart, err := s.repo.GetSeasonStartDate()
	if err != nil {
		return nil, err
	}

	matches, err := s.repo.GetAllAfter(seasonStart)
	if err != nil {
		return nil, err
	}

	playerResets, _ := s.repo.GetPlayerResetDates()
	if playerResets == nil {
		playerResets = make(map[string]time.Time)
	}

	f := excelize.NewFile()
	sheet := "Report"
	f.NewSheet(sheet)
	f.DeleteSheet("Sheet1")

	headers := []string{"ID", "Date", "Player", "Result", "K/D/A", "Champion"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sheet, cell, h)
	}

	row := 2
	for _, m := range matches {
		for _, p := range m.Players {
			if pReset, ok := playerResets[p.PlayerName]; ok {
				if m.CreatedAt.Before(pReset) {
					continue
				}
			}

			f.SetCellValue(sheet, fmt.Sprintf("A%d", row), m.ID)
			f.SetCellValue(sheet, fmt.Sprintf("B%d", row), m.CreatedAt.Format("2006-01-02 15:04"))
			f.SetCellValue(sheet, fmt.Sprintf("C%d", row), p.PlayerName)
			f.SetCellValue(sheet, fmt.Sprintf("D%d", row), p.Result)
			f.SetCellValue(sheet, fmt.Sprintf("E%d", row), fmt.Sprintf("%d/%d/%d", p.Kills, p.Deaths, p.Assists))
			f.SetCellValue(sheet, fmt.Sprintf("F%d", row), p.Champion)
			row++
		}
	}

	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
