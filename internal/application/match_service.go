package application

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
	"valhalla/internal/integration"
	"valhalla/internal/models"
	"valhalla/internal/repository"

	"github.com/xuri/excelize/v2"
)

type MatchServiceImpl struct {
	repo       repository.Match
	ai         AIProvider
	sheets     *integration.SheetService
	ownerEmail string
	logger     Logger
}

func NewMatchServiceImpl(repo repository.Match, ai AIProvider, sheets *integration.SheetService, ownerEmail string, logger Logger) *MatchServiceImpl {
	return &MatchServiceImpl{
		repo:       repo,
		ai:         ai,
		sheets:     sheets,
		ownerEmail: ownerEmail,
		logger:     logger,
	}
}

type PlayerStats struct {
	Name    string
	Matches int
	Wins    int
	Losses  int
	Kills   int
	Deaths  int
	Assists int
}

func (s *MatchServiceImpl) ProcessImage(data []byte) error {
	hash := sha256.Sum256(data)
	fileHash := hex.EncodeToString(hash[:])

	exists, err := s.repo.Exists(fileHash, "")
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("duplicate match detected")
	}

	match, err := s.ai.ParseImage(data)
	if err != nil {
		return err
	}
	match.FileHash = fileHash

	matchSig := generateSignature(match)
	match.MatchSignature = matchSig
	sigExists, err := s.repo.Exists("", matchSig)
	if err != nil {
		return err
	}
	if sigExists {
		return fmt.Errorf("duplicate match detected")
	}

	_, err = s.repo.Create(*match)
	if err != nil {
		return err
	}

	if s.sheets != nil {
		s.sheets.SetSpreadsheetID("1ZDBqKL1Sgr8-JPXChMafyiHmzHXVJB0aFKXgoTjEfR8")
		go func() {
			_, err := s.SyncToGoogleSheet()
			if err != nil {
				s.logger.Error("Auto-sync failed: %v", err)
			}
		}()
	}

	return nil
}

func (s *MatchServiceImpl) GetLeaderboard(sortBy string) ([]*PlayerStats, error) {
	statsList, err := s.calculateStats()
	if err != nil {
		return nil, err
	}

	sort.Slice(statsList, func(i, j int) bool {
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

		wr1 := 0.0
		if statsList[i].Matches > 0 {
			wr1 = float64(statsList[i].Wins) / float64(statsList[i].Matches)
		}

		wr2 := 0.0
		if statsList[j].Matches > 0 {
			wr2 = float64(statsList[j].Wins) / float64(statsList[j].Matches)
		}

		if sortBy == "winrate" {
			if wr1 != wr2 {
				return wr1 > wr2
			}
			return kda1 > kda2
		}

		if kda1 != kda2 {
			return kda1 > kda2
		}
		return wr1 > wr2
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
	name, err := s.repo.GetPlayerNameByID(id)
	if err != nil {
		return nil, fmt.Errorf("игрок не найден")
	}

	matches, err := s.repo.GetHistory(name, 10)
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

func (s *MatchServiceImpl) SyncToGoogleSheet() (string, error) {
	if s.sheets == nil {
		return "", fmt.Errorf("google sheets service is not disabled")
	}

	s.sheets.SetSpreadsheetID("1ZDBqKL1Sgr8-JPXChMafyiHmzHXVJB0aFKXgoTjEfR8")

	statsList, err := s.calculateStats()
	if err != nil {
		return "", err
	}

	sort.Slice(statsList, func(i, j int) bool {
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
	rows = append(rows, []interface{}{"Rank", "Player", "Matches", "Wins", "Losses", "WinRate %", "KDA"})

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
			st.Name,
			st.Matches,
			st.Wins,
			st.Losses,
			fmt.Sprintf("%.1f%%", winRate),
			fmt.Sprintf("%.2f", kdaRatio),
		})
	}

	if _, _, err := s.sheets.EnsureSheetExists("Valhalla Leaderboard 🏆", s.ownerEmail); err != nil {
	}

	if err := s.sheets.UpdateStats(rows); err != nil {
		return "", fmt.Errorf("failed to update stats: %w", err)
	}

	return fmt.Sprintf("https://docs.google.com/spreadsheets/d/%s", "1ZDBqKL1Sgr8-JPXChMafyiHmzHXVJB0aFKXgoTjEfR8"), nil
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

	statsMap := make(map[string]*PlayerStats)

	for _, m := range matches {
		for _, p := range m.Players {
			if pReset, ok := playerResets[p.PlayerName]; ok {
				if m.CreatedAt.Before(pReset) {
					continue
				}
			}

			if _, exists := statsMap[p.PlayerName]; !exists {
				statsMap[p.PlayerName] = &PlayerStats{Name: p.PlayerName}
			}

			stat := statsMap[p.PlayerName]
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
	if s.sheets != nil {
		headers := [][]interface{}{
			{"Rank", "Player", "Matches", "Wins", "Losses", "WinRate %", "KDA"},
		}
		_ = s.sheets.UpdateStats(headers)
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

	headers := []string{"Player", "Matches", "Wins", "Losses", "WinRate %", "KDA"}
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

		f.SetCellValue(sheet, fmt.Sprintf("A%d", row), st.Name)
		f.SetCellValue(sheet, fmt.Sprintf("B%d", row), st.Matches)
		f.SetCellValue(sheet, fmt.Sprintf("C%d", row), st.Wins)
		f.SetCellValue(sheet, fmt.Sprintf("D%d", row), st.Losses)
		f.SetCellValue(sheet, fmt.Sprintf("E%d", row), fmt.Sprintf("%.1f%%", winRate))
		f.SetCellValue(sheet, fmt.Sprintf("F%d", row), fmt.Sprintf("%.2f", kdaRatio))
		row++
	}

	f.SetColWidth(sheet, "A", "A", 20)
	f.SetColWidth(sheet, "B", "F", 12)

	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
