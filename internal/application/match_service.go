package application

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
	"valhalla/internal/models"
	"valhalla/internal/repository"
	"valhalla/pkg/sheets"

	"github.com/xuri/excelize/v2"
)

type MatchServiceImpl struct {
	repo          repository.Match
	ai            AIProvider
	sheetsClient  sheets.Client
	spreadsheetID string
	ownerEmail    string
	logger        Logger
}

func NewMatchServiceImpl(repo repository.Match, ai AIProvider, sheetsClient sheets.Client, ownerEmail string, logger Logger) *MatchServiceImpl {
	return &MatchServiceImpl{
		repo:          repo,
		ai:            ai,
		sheetsClient:  sheetsClient,
		spreadsheetID: "1ZDBqKL1Sgr8-JPXChMafyiHmzHXVJB0aFKXgoTjEfR8",
		ownerEmail:    ownerEmail,
		logger:        logger,
	}
}

type PlayerStats struct {
	ID      int
	Name    string
	Matches int
	Wins    int
	Losses  int
	Kills   int
	Deaths  int
	Assists int
}

func (s *MatchServiceImpl) ProcessImage(data []byte) (int, error) {
	hash := sha256.Sum256(data)
	fileHash := hex.EncodeToString(hash[:])

	exists, err := s.repo.Exists(fileHash, "")
	if err != nil {
		return 0, err
	}
	if exists {
		return 0, fmt.Errorf("duplicate match detected")
	}

	match, err := s.ai.ParseImage(data)
	if err != nil {
		return 0, err
	}
	match.FileHash = fileHash

	matchSig := generateSignature(match)
	match.MatchSignature = matchSig
	sigExists, err := s.repo.Exists("", matchSig)
	if err != nil {
		return 0, err
	}
	if sigExists {
		return 0, fmt.Errorf("duplicate match detected")
	}

	matchID, err := s.repo.Create(*match)
	if err != nil {
		return 0, err
	}

	if s.sheetsClient != nil {
		go func() {
			_, err := s.SyncToGoogleSheet()
			if err != nil {
				s.logger.Error("Auto-sync failed: %v", err)
			}
		}()
	}

	return matchID, nil
}

func (s *MatchServiceImpl) ProcessImageFromURL(url string) (int, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := client.Get(url)
	if err != nil {
		return 0, fmt.Errorf("failed to download image: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return 0, fmt.Errorf("failed to read image body: %w", err)
	}

	return s.ProcessImage(data)
}

func (s *MatchServiceImpl) GetLeaderboard(sortBy string) ([]*PlayerStats, error) {
	statsList, err := s.calculateStats()
	if err != nil {
		return nil, err
	}

	sort.Slice(statsList, func(i, j int) bool {
		m1 := statsList[i].Matches
		m2 := statsList[j].Matches

		if m1 != m2 {
			return m1 > m2
		}

		wr1 := 0.0
		if m1 > 0 {
			wr1 = float64(statsList[i].Wins) / float64(m1)
		}

		wr2 := 0.0
		if m2 > 0 {
			wr2 = float64(statsList[j].Wins) / float64(m2)
		}

		if wr1 != wr2 {
			return wr1 > wr2
		}

		d1 := statsList[i].Deaths
		if d1 == 0 {
			d1 = 1
		}
		kda1 := float64(statsList[i].Kills+statsList[i].Assists) / float64(d1)

		d2 := statsList[j].Deaths
		if d2 == 0 {
			d2 = 1
		}
		kda2 := float64(statsList[j].Kills+statsList[j].Assists) / float64(d2)

		return kda1 > kda2
	})

	return statsList, nil
}

func (s *MatchServiceImpl) GetPlayerList() ([]models.Player, error) {
	return s.repo.GetAllPlayers()
}

func (s *MatchServiceImpl) GetPlayerNameByID(id int) (string, error) {
	return s.repo.GetPlayerNameByID(id)
}

func (s *MatchServiceImpl) GetHistoryByID(id int) ([]string, error) {
	matches, err := s.repo.GetHistory(id, 10)
	if err != nil {
		return nil, err
	}

	var lines []string
	for _, m := range matches {
		p := m.Players[0]
		line := fmt.Sprintf("🆔 **%d** | %s | ⚔️ %d/%d/%d | %s",
			m.ID, p.Result, p.Kills, p.Deaths, p.Assists, m.CreatedAt.Format("02.01"))
		lines = append(lines, line)
	}
	return lines, nil
}

func (s *MatchServiceImpl) WipePlayerByID(id int) error {
	return s.repo.WipePlayerByID(id)
}

func (s *MatchServiceImpl) RenamePlayer(id int, newName string) error {
	return s.repo.RenamePlayer(id, newName)
}

func (s *MatchServiceImpl) GetPlayerStats(name string) (*PlayerStats, error) {
	stats, err := s.calculateStats()
	if err != nil {
		return nil, err
	}

	for _, st := range stats {
		if strings.EqualFold(st.Name, name) {
			return st, nil
		}
	}
	return nil, fmt.Errorf("игрок не найден")
}

func (s *MatchServiceImpl) GetPlayerStatsByID(id int) (*PlayerStats, error) {
	stats, err := s.calculateStats()
	if err != nil {
		return nil, err
	}

	for _, st := range stats {
		if st.ID == id {
			return st, nil
		}
	}
	return nil, fmt.Errorf("игрок не найден")
}

func (s *MatchServiceImpl) SyncToGoogleSheet() (string, error) {
	if s.sheetsClient == nil {
		return "", fmt.Errorf("google sheets service is not configured")
	}

	statsList, err := s.calculateStats()
	if err != nil {
		return "", err
	}

	sort.Slice(statsList, func(i, j int) bool {
		m1 := statsList[i].Matches
		m2 := statsList[j].Matches

		if m1 != m2 {
			return m1 > m2
		}

		wr1 := 0.0
		if m1 > 0 {
			wr1 = float64(statsList[i].Wins) / float64(m1)
		}
		wr2 := 0.0
		if m2 > 0 {
			wr2 = float64(statsList[j].Wins) / float64(m2)
		}

		if wr1 != wr2 {
			return wr1 > wr2
		}

		d1 := statsList[i].Deaths
		if d1 == 0 {
			d1 = 1
		}
		kda1 := float64(statsList[i].Kills+statsList[i].Assists) / float64(d1)
		d2 := statsList[j].Deaths
		if d2 == 0 {
			d2 = 1
		}
		kda2 := float64(statsList[j].Kills+statsList[j].Assists) / float64(d2)
		return kda1 > kda2
	})

	var rows [][]interface{}
	rows = append(rows, []interface{}{"Rank", "ID", "Player", "Matches", "Wins", "Losses", "WinRate %", "KDA"})

	for i, st := range statsList {
		winRate := 0.0
		if st.Matches > 0 {
			winRate = (float64(st.Wins) / float64(st.Matches)) * 100
		}

		deaths := st.Deaths
		if deaths == 0 {
			deaths = 1
		}
		kdaRatio := float64(st.Kills+st.Assists) / float64(deaths)

		rows = append(rows, []interface{}{
			i + 1,
			st.ID,
			st.Name,
			st.Matches,
			st.Wins,
			st.Losses,
			fmt.Sprintf("%.1f%%", winRate),
			fmt.Sprintf("%.2f", kdaRatio),
		})
	}

	if err := s.sheetsClient.ClearRange(s.spreadsheetID, "A1:Z1000"); err != nil {
		s.logger.Error("failed to clear sheet: %v", err)
	}

	if err := s.sheetsClient.UpdateValues(s.spreadsheetID, "A1", rows); err != nil {
		return "", fmt.Errorf("failed to update stats: %w", err)
	}

	return fmt.Sprintf("https://docs.google.com/spreadsheets/d/%s", s.spreadsheetID), nil
}

func (s *MatchServiceImpl) calculateStats() ([]*PlayerStats, error) {
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

	statsMap := make(map[int]*PlayerStats)

	for _, m := range matches {
		for _, p := range m.Players {
			if pReset, ok := playerResets[p.PlayerName]; ok {
				if m.CreatedAt.Before(pReset) {
					continue
				}
			}

			if _, exists := statsMap[p.PlayerID]; !exists {
				statsMap[p.PlayerID] = &PlayerStats{
					ID:   p.PlayerID,
					Name: p.PlayerName,
				}
			}

			stat := statsMap[p.PlayerID]
			stat.Matches++
			stat.Kills += p.Kills
			stat.Deaths += p.Deaths
			stat.Assists += p.Assists

			if strings.EqualFold(p.Result, "WIN") {
				stat.Wins++
			} else {
				stat.Losses++
			}
		}
	}

	var statsList []*PlayerStats
	for _, st := range statsMap {
		statsList = append(statsList, st)
	}
	return statsList, nil
}

func generateSignature(m *models.Match) string {
	var sb strings.Builder
	for _, p := range m.Players {
		sb.WriteString(fmt.Sprintf("%s-%s-%d-%d-%d|", p.PlayerName, p.Result, p.Kills, p.Deaths, p.Assists))
	}
	return sb.String()
}

func (s *MatchServiceImpl) SetTimer(dateStr string) error {
	layout := "2006-01-02"
	t, err := time.Parse(layout, dateStr)
	if err != nil {
		return fmt.Errorf("неверный формат даты, используйте YYYY-MM-DD")
	}
	return s.repo.SetSeasonStartDate(t)
}

func (s *MatchServiceImpl) ResetGlobal() error {
	return s.repo.SetSeasonStartDate(time.Now())
}

func (s *MatchServiceImpl) ResetPlayer(name, dateStr string) error {
	var t time.Time
	if dateStr == "now" {
		t = time.Now()
	} else {
		var err error
		t, err = time.Parse("2006-01-02", dateStr)
		if err != nil {
			return fmt.Errorf("неверный формат даты")
		}
	}
	return s.repo.SetPlayerResetDate(name, t)
}

func (s *MatchServiceImpl) DeleteMatch(id int) error {
	return s.repo.Delete(id)
}

func (s *MatchServiceImpl) WipeAllData() error {
	if err := s.repo.WipeAll(); err != nil {
		return fmt.Errorf("ошибка очистки БД: %w", err)
	}
	if s.sheetsClient != nil {
		headers := [][]interface{}{
			{"Rank", "ID", "Player", "Matches", "Wins", "Losses", "WinRate %", "KDA"},
		}
		_ = s.sheetsClient.ClearRange(s.spreadsheetID, "A1:Z1000")
		_ = s.sheetsClient.UpdateValues(s.spreadsheetID, "A1", headers)
	}
	_ = s.repo.SetSeasonStartDate(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	return nil
}

func (s *MatchServiceImpl) GetExcelReport() ([]byte, error) {
	statsList, err := s.calculateStats()
	if err != nil {
		return nil, err
	}

	f := excelize.NewFile()
	sheet := "Leaderboard"
	f.NewSheet(sheet)
	f.DeleteSheet("Sheet1")

	headers := []string{"ID", "Player", "Matches", "Wins", "Losses", "WinRate %", "KDA"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sheet, cell, h)
	}

	row := 2
	for _, st := range statsList {
		winRate := 0.0
		if st.Matches > 0 {
			winRate = (float64(st.Wins) / float64(st.Matches)) * 100
		}
		deaths := st.Deaths
		if deaths == 0 {
			deaths = 1
		}
		kdaRatio := float64(st.Kills+st.Assists) / float64(deaths)

		f.SetCellValue(sheet, fmt.Sprintf("A%d", row), st.ID)
		f.SetCellValue(sheet, fmt.Sprintf("B%d", row), st.Name)
		f.SetCellValue(sheet, fmt.Sprintf("C%d", row), st.Matches)
		f.SetCellValue(sheet, fmt.Sprintf("D%d", row), st.Wins)
		f.SetCellValue(sheet, fmt.Sprintf("E%d", row), st.Losses)
		f.SetCellValue(sheet, fmt.Sprintf("F%d", row), fmt.Sprintf("%.1f%%", winRate))
		f.SetCellValue(sheet, fmt.Sprintf("G%d", row), fmt.Sprintf("%.2f", kdaRatio))
		row++
	}

	f.SetColWidth(sheet, "A", "A", 10)
	f.SetColWidth(sheet, "B", "B", 20)
	f.SetColWidth(sheet, "C", "G", 12)

	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
