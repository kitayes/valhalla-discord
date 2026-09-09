package application

import (
	"blackwatch/internal/domain"
	"blackwatch/internal/models"
	"blackwatch/internal/repository"
	"blackwatch/pkg/sheets"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/xuri/excelize/v2"
)

type MatchServiceImpl struct {
	repo          repository.Match
	ai            AIProvider
	sheetsClient  sheets.Client
	spreadsheetID string
	ownerEmail    string
	httpTimeout   time.Duration
	statsCache    *StatsCache
	logger        Logger
	syncSem       chan struct{}
	syncWg        sync.WaitGroup
}

func NewMatchServiceImpl(repo repository.Match, ai AIProvider, sheetsClient sheets.Client, ownerEmail, spreadsheetID string, httpTimeoutSec int, logger Logger) *MatchServiceImpl {
	return &MatchServiceImpl{
		repo:          repo,
		ai:            ai,
		sheetsClient:  sheetsClient,
		spreadsheetID: spreadsheetID,
		ownerEmail:    ownerEmail,
		httpTimeout:   time.Duration(httpTimeoutSec) * time.Second,
		statsCache:    NewStatsCache(time.Duration(defaultStatsCacheTTL) * time.Second),
		logger:        logger,
		syncSem:       make(chan struct{}, 2), // Max 2 concurrent syncs
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
	MVP     int // MVP medals earned this season
	SVP     int // SVPG medals earned this season
}

func (s *MatchServiceImpl) ProcessImage(ctx context.Context, data []byte) (*models.MatchResult, error) {
	return s.ProcessImageWithPlayers(ctx, data, nil)
}

// ProcessImageWithPlayers processes a screenshot with the expected player list
// for improved OCR accuracy via dynamic prompt injection. The returned result
// carries the MVP/SVPG names the AI read off the scoreboard, so callers can
// attach them to the lobby match they belong to.
func (s *MatchServiceImpl) ProcessImageWithPlayers(ctx context.Context, data []byte, expectedPlayers []string) (*models.MatchResult, error) {
	hash := sha256.Sum256(data)
	fileHash := hex.EncodeToString(hash[:])

	exists, err := s.repo.Exists(ctx, fileHash, "")
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, fmt.Errorf("file hash %s: %w", fileHash, domain.ErrDuplicateMatch)
	}

	match, err := s.ai.ParseImageWithPlayers(ctx, data, expectedPlayers)
	if err != nil {
		return nil, err
	}
	match.FileHash = fileHash

	matchSig := generateSignature(match)
	match.MatchSignature = matchSig
	sigExists, err := s.repo.Exists(ctx, "", matchSig)
	if err != nil {
		return nil, err
	}
	if sigExists {
		return nil, fmt.Errorf("signature %s: %w", matchSig, domain.ErrDuplicateMatch)
	}

	matchID, err := s.repo.Create(ctx, *match)
	if err != nil {
		return nil, err
	}

	s.statsCache.Invalidate()

	if match.MVP != "" || match.SVP != "" {
		s.logger.Info("match #%d medals — MVP: %q, SVP: %q", matchID, match.MVP, match.SVP)
	}

	if s.sheetsClient != nil {
		// Deliberately NOT ctx: the caller cancels it as soon as the upload
		// response is sent, which would abort the sync before it started.
		s.syncWg.Add(1)
		go func() {
			defer s.syncWg.Done()

			syncCtx, cancel := context.WithTimeout(context.Background(), sheetSyncTimeout)
			defer cancel()

			if _, err := s.SyncToGoogleSheet(syncCtx); err != nil {
				s.logger.Error("Auto-sync failed: %v", err)
			}
		}()
	}

	return &models.MatchResult{MatchID: matchID, MVP: match.MVP, SVP: match.SVP}, nil
}

func (s *MatchServiceImpl) ProcessImageFromURL(ctx context.Context, url string) (*models.MatchResult, error) {
	return s.ProcessImageFromURLWithPlayers(ctx, url, nil)
}

// ProcessImageFromURLWithPlayers downloads and processes an image from a URL,
// injecting expected player names into the AI prompt for improved OCR accuracy.
func (s *MatchServiceImpl) ProcessImageFromURLWithPlayers(ctx context.Context, url string, expectedPlayers []string) (*models.MatchResult, error) {
	client := &http.Client{
		Timeout: s.httpTimeout,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build image request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download image: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort cleanup

	if resp.ContentLength > 0 && resp.ContentLength > maxImageDownloadSize {
		return nil, fmt.Errorf("image too large: %d bytes exceeds maximum %d bytes",
			resp.ContentLength, maxImageDownloadSize)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageDownloadSize))
	if err != nil {
		return nil, fmt.Errorf("failed to read image body: %w", err)
	}

	if len(data) >= maxImageDownloadSize {
		return nil, fmt.Errorf("image size exceeds maximum allowed size of %d bytes", maxImageDownloadSize)
	}

	return s.ProcessImageWithPlayers(ctx, data, expectedPlayers)
}

// GetLeaderboard returns the season standings. sortBy selects the ordering:
// "winrate" ranks by win rate first, anything else falls back to the default
// matches → win rate → KDA priority. The parameter used to be accepted and then
// ignored, so /top sort:winrate returned the default board under a title that
// claimed otherwise.
func (s *MatchServiceImpl) GetLeaderboard(ctx context.Context, sortBy string) ([]*PlayerStats, error) {
	statsList, err := s.calculateStats(ctx)
	if err != nil {
		return nil, err
	}

	less := comparePlayersByPriority
	if strings.EqualFold(sortBy, "winrate") {
		less = comparePlayersByWinRate
	}

	sort.Slice(statsList, func(i, j int) bool {
		return less(statsList[i], statsList[j])
	})

	return statsList, nil
}

func (s *MatchServiceImpl) GetPlayerList(ctx context.Context) ([]models.Player, error) {
	return s.repo.GetAllPlayers(ctx)
}

func (s *MatchServiceImpl) GetPlayerNameByID(ctx context.Context, id int) (string, error) {
	return s.repo.GetPlayerNameByID(ctx, id)
}

// GetPlayerNamesByIDs resolves a batch of players in one query. Callers that
// render a roster used to loop over GetPlayerNameByID, one round trip each.
func (s *MatchServiceImpl) GetPlayerNamesByIDs(ctx context.Context, ids []int) (map[int]string, error) {
	return s.repo.GetPlayerNamesByIDs(ctx, ids)
}

// GetDiscordIDByPlayerID returns the discord_id linked to a player.
func (s *MatchServiceImpl) GetDiscordIDByPlayerID(ctx context.Context, playerID int) (string, error) {
	return s.repo.GetDiscordIDByPlayerID(ctx, playerID)
}

// GetPlayerByDiscordID returns the player ID and name for a given discord_id (O(1) SQL).
func (s *MatchServiceImpl) GetPlayerByDiscordID(ctx context.Context, discordID string) (int, string, error) {
	return s.repo.GetPlayerByDiscordID(ctx, discordID)
}

// BindDiscordID links a Discord account to a player profile.
//
// Without a binding the whole lobby flow is unreachable — the join button, the
// requeue button, tier roles and rank announcements all resolve a player through
// discord_id, and nothing ever wrote to that column.
//
// force is for admins: a self-service bind refuses to touch a profile that is
// already claimed, or to hand a second profile to an account that already has
// one. Returns the player's name.
func (s *MatchServiceImpl) BindDiscordID(ctx context.Context, playerID int, discordID string, force bool) (string, error) {
	name, err := s.repo.GetPlayerNameByID(ctx, playerID)
	if err != nil {
		return "", fmt.Errorf("player %d: %w", playerID, err)
	}

	if !force {
		// Both guards are fail-closed. Treating any error as "not bound" meant a
		// single dropped connection let a caller take over a profile that
		// already belonged to somebody else: SetDiscordID overwrites the column,
		// the unique index stays satisfied, and the previous owner silently
		// loses their binding.
		boundID, boundName, err := s.repo.GetPlayerByDiscordID(ctx, discordID)
		switch {
		case err == nil && boundID == playerID:
			return name, nil // already bound to this very profile
		case err == nil:
			return "", fmt.Errorf("профиль **%s** (ID: %d): %w", boundName, boundID, domain.ErrDiscordAlreadyBound)
		case !errors.Is(err, domain.ErrPlayerNotFound):
			return "", fmt.Errorf("failed to check existing binding for discord %s: %w", discordID, err)
		}

		existing, err := s.repo.GetDiscordIDByPlayerID(ctx, playerID)
		switch {
		case err == nil && existing != "":
			return "", fmt.Errorf("профиль **%s**: %w", name, domain.ErrProfileTaken)
		case err != nil && !errors.Is(err, domain.ErrDiscordNotLinked):
			return "", fmt.Errorf("failed to check profile %d: %w", playerID, err)
		}
	}

	if err := s.repo.SetDiscordID(ctx, playerID, discordID); err != nil {
		// Translated here, at the storage boundary. Returning the repository's
		// own sentinel made the delivery layer import internal/repository to
		// branch on it, and the flat fmt.Errorf that replaced it dropped %w
		// entirely — so every errors.Is in bindFailureMessage missed and the
		// user got "try again later" for a permanent condition.
		if errors.Is(err, repository.ErrDiscordIDTaken) {
			return "", fmt.Errorf("discord %s: %w", discordID, domain.ErrDiscordAlreadyBound)
		}
		return "", err
	}

	s.logger.Info("bind: discord %s linked to player %s (ID: %d, force: %t)", discordID, name, playerID, force)
	return name, nil
}

// FindPlayerByName resolves a nickname to a player without creating anything.
func (s *MatchServiceImpl) FindPlayerByName(ctx context.Context, nickname string) (int, string, error) {
	return s.repo.FindPlayerByExactName(ctx, strings.TrimSpace(nickname))
}

// SuggestPlayerNames returns nicknames starting with or containing the prefix,
// for Discord's autocomplete. Discord allows at most 25 choices and gives the
// bot three seconds to answer, so the query is capped and ordered rather than
// filtered in memory.
func (s *MatchServiceImpl) SuggestPlayerNames(ctx context.Context, prefix string, limit int) ([]models.Player, error) {
	return s.repo.SuggestPlayerNames(ctx, strings.TrimSpace(prefix), limit)
}

// BindDiscordByName claims the profile carrying this nickname, creating it when
// nobody has played under that name yet. Reports whether the profile was created.
//
// Registration used to be impossible for a new player. A players row was only
// ever born inside Create, from a nickname the parser read off a match
// screenshot — so joining the lobby needed a bound profile, binding needed an
// existing profile, and an existing profile needed a match you could not join.
// Anyone who had never played was locked out permanently.
//
// The order below matters: every refusal happens before the single write, so a
// rejected binding can never leave an empty profile behind.
func (s *MatchServiceImpl) BindDiscordByName(ctx context.Context, nickname, discordID string) (int, bool, error) {
	if err := domain.ValidatePlayerName(nickname); err != nil {
		return 0, false, err
	}
	nickname = strings.TrimSpace(nickname)

	// The caller's own binding is checked first, before anything is created:
	// somebody who already owns a profile must not be able to spawn a second
	// one by typing a fresh nickname.
	if boundID, boundName, err := s.repo.GetPlayerByDiscordID(ctx, discordID); err == nil {
		existingID, _, findErr := s.repo.FindPlayerByExactName(ctx, nickname)
		if findErr == nil && existingID == boundID {
			return boundID, false, nil // already bound to this very profile
		}
		return 0, false, fmt.Errorf("профиль **%s** (ID: %d): %w", boundName, boundID, domain.ErrDiscordAlreadyBound)
	} else if !errors.Is(err, domain.ErrPlayerNotFound) {
		return 0, false, fmt.Errorf("failed to check existing binding for discord %s: %w", discordID, err)
	}

	playerID, storedName, err := s.repo.FindPlayerByExactName(ctx, nickname)
	switch {
	case err == nil:
		// The nickname is known: claim it unless somebody already has.
		existing, discErr := s.repo.GetDiscordIDByPlayerID(ctx, playerID)
		switch {
		case discErr == nil && existing != "":
			return 0, false, fmt.Errorf("профиль **%s**: %w", storedName, domain.ErrProfileTaken)
		case discErr != nil && !errors.Is(discErr, domain.ErrDiscordNotLinked):
			return 0, false, fmt.Errorf("failed to check profile %d: %w", playerID, discErr)
		}
		if err := s.repo.SetDiscordID(ctx, playerID, discordID); err != nil {
			if errors.Is(err, repository.ErrDiscordIDTaken) {
				return 0, false, fmt.Errorf("discord %s: %w", discordID, domain.ErrDiscordAlreadyBound)
			}
			return 0, false, err
		}
		s.logger.Info("bind: discord %s claimed existing profile %s (ID: %d)", discordID, storedName, playerID)
		return playerID, false, nil

	case errors.Is(err, domain.ErrPlayerNotFound):
		newID, createErr := s.repo.CreatePlayerWithDiscord(ctx, nickname, discordID)
		if createErr != nil {
			if errors.Is(createErr, repository.ErrDiscordIDTaken) {
				return 0, false, fmt.Errorf("discord %s: %w", discordID, domain.ErrDiscordAlreadyBound)
			}
			return 0, false, createErr
		}
		s.logger.Info("bind: discord %s registered new profile %s (ID: %d)", discordID, nickname, newID)
		return newID, true, nil

	default:
		return 0, false, fmt.Errorf("failed to resolve nickname %q: %w", nickname, err)
	}
}

// UnbindDiscordID releases a player's Discord binding.
func (s *MatchServiceImpl) UnbindDiscordID(ctx context.Context, playerID int) (string, error) {
	name, err := s.repo.GetPlayerNameByID(ctx, playerID)
	if err != nil {
		return "", fmt.Errorf("player %d: %w", playerID, err)
	}
	if err := s.repo.ClearDiscordID(ctx, playerID); err != nil {
		return "", err
	}
	s.logger.Info("bind: player %s (ID: %d) unlinked from Discord", name, playerID)
	return name, nil
}

func (s *MatchServiceImpl) GetHistoryByID(ctx context.Context, id int) ([]string, error) {
	matches, err := s.repo.GetHistory(ctx, id, defaultHistoryLimit)
	if err != nil {
		return nil, err
	}

	var lines []string
	for _, m := range matches {
		// The repository builds one entry per match holding just this player's
		// row, but an empty Players slice must not take the handler down.
		if len(m.Players) == 0 {
			continue
		}
		p := m.Players[0]
		line := fmt.Sprintf("🆔 %d | %s | ⚔️ %d/%d/%d | %s",
			m.ID, p.Result, p.Kills, p.Deaths, p.Assists, m.CreatedAt.Format("02.01"))
		lines = append(lines, line)
	}
	return lines, nil
}

func (s *MatchServiceImpl) WipePlayerByID(ctx context.Context, id int) error {
	return s.repo.WipePlayerByID(ctx, id)
}

func (s *MatchServiceImpl) RenamePlayer(ctx context.Context, id int, newName string) error {
	return s.repo.RenamePlayer(ctx, id, newName)
}

func (s *MatchServiceImpl) GetPlayerStats(ctx context.Context, name string) (*PlayerStats, error) {
	stats, err := s.calculateStats(ctx)
	if err != nil {
		return nil, err
	}

	for _, st := range stats {
		if strings.EqualFold(st.Name, name) {
			return st, nil
		}
	}
	return nil, fmt.Errorf("player %q: %w", name, domain.ErrPlayerNotFound)
}

func (s *MatchServiceImpl) GetPlayerStatsByID(ctx context.Context, id int) (*PlayerStats, error) {
	stats, err := s.calculateStats(ctx)
	if err != nil {
		return nil, err
	}

	for _, st := range stats {
		if st.ID == id {
			return st, nil
		}
	}
	return nil, fmt.Errorf("player %d: %w", id, domain.ErrPlayerNotFound)
}

func (s *MatchServiceImpl) SyncToGoogleSheet(ctx context.Context) (string, error) {
	if s.sheetsClient == nil {
		return "", fmt.Errorf("google sheets service is not configured")
	}

	statsList, err := s.calculateStats(ctx)
	if err != nil {
		return "", err
	}

	sort.Slice(statsList, func(i, j int) bool {
		return comparePlayersByPriority(statsList[i], statsList[j])
	})

	var rows [][]interface{}
	rows = append(rows, []interface{}{"Rank", "ID", "Player", "Matches", "Wins", "Losses", "WinRate %", "KDA"})

	for i, st := range statsList {
		winRate := calculateWinRate(st.Wins, st.Matches)
		kdaRatio := calculateKDA(st.Kills, st.Deaths, st.Assists)

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

func (s *MatchServiceImpl) calculateStats(ctx context.Context) ([]*PlayerStats, error) {
	if cached, ok := s.statsCache.Get(); ok {
		return cached, nil
	}

	seasonStart, err := s.repo.GetSeasonStartDate(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get season start date: %w", err)
	}

	matches, err := s.repo.GetAllAfter(ctx, seasonStart)
	if err != nil {
		return nil, fmt.Errorf("failed to get matches: %w", err)
	}

	playerResets, err := s.repo.GetPlayerResetDates(ctx)
	if err != nil {
		// Log the error but continue with empty map
		s.logger.Warn("failed to get player reset dates: %v, continuing without resets", err)
		playerResets = make(map[string]time.Time)
	}
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

	// Medal tallies are scoped to the same season window as the rest of the
	// stats; players.mvp_count stays as the all-time counter.
	medals, err := s.repo.GetMedalCountsAfter(ctx, seasonStart)
	if err != nil {
		s.logger.Warn("failed to get medal counts: %v, continuing without medals", err)
	} else {
		for playerID, m := range medals {
			if stat, ok := statsMap[playerID]; ok {
				stat.MVP = m.MVP
				stat.SVP = m.SVP
			}
		}
	}

	var statsList []*PlayerStats
	for _, st := range statsMap {
		statsList = append(statsList, st)
	}

	s.statsCache.Set(statsList)
	return statsList, nil
}

// GetLifetimeMedals returns a player's all-time MVP/SVPG counters.
func (s *MatchServiceImpl) GetLifetimeMedals(ctx context.Context, playerID int) (models.Medals, error) {
	return s.repo.GetLifetimeMedals(ctx, playerID)
}

func generateSignature(m *models.Match) string {
	var sb strings.Builder
	for _, p := range m.Players {
		sb.WriteString(fmt.Sprintf("%s-%s-%d-%d-%d%s", p.PlayerName, p.Result, p.Kills, p.Deaths, p.Assists, signatureSeparator))
	}
	return sb.String()
}

func (s *MatchServiceImpl) SetTimer(ctx context.Context, dateStr string) error {
	layout := "2006-01-02"
	t, err := time.Parse(layout, dateStr)
	if err != nil {
		return fmt.Errorf("неверный формат даты, используйте YYYY-MM-DD")
	}
	return s.repo.SetSeasonStartDate(ctx, t)
}

func (s *MatchServiceImpl) ResetGlobal(ctx context.Context) error {
	return s.repo.SetSeasonStartDate(ctx, time.Now())
}

func (s *MatchServiceImpl) ResetPlayer(ctx context.Context, name, dateStr string) error {
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
	return s.repo.SetPlayerResetDate(ctx, name, t)
}

func (s *MatchServiceImpl) DeleteMatch(ctx context.Context, id int) error {
	return s.repo.Delete(ctx, id)
}

func (s *MatchServiceImpl) WipeAllData(ctx context.Context) error {
	if err := s.repo.WipeAll(ctx); err != nil {
		return fmt.Errorf("ошибка очистки БД: %w", err)
	}
	// The database is already gone, so nothing below is worth aborting on — but
	// nothing below may be silently discarded either. A blanket `_ =` told the
	// admin "готово" while the season date was still set and the sheet still
	// held the old table; errcheck does not flag an explicit `_ =`, so the
	// linter added in this change would never have caught it.
	if s.sheetsClient != nil {
		headers := [][]interface{}{
			{"Rank", "ID", "Player", "Matches", "Wins", "Losses", "WinRate %", "KDA"},
		}
		if err := s.sheetsClient.ClearRange(s.spreadsheetID, "A1:Z1000"); err != nil {
			s.logger.Error("wipe: failed to clear sheet %s: %v", s.spreadsheetID, err)
		} else if err := s.sheetsClient.UpdateValues(s.spreadsheetID, "A1", headers); err != nil {
			s.logger.Error("wipe: failed to restore sheet headers in %s: %v", s.spreadsheetID, err)
		}
	}
	if err := s.repo.SetSeasonStartDate(ctx, time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		s.logger.Error("wipe: failed to reset season start date: %v", err)
	}
	return nil
}

func (s *MatchServiceImpl) GetExcelReport(ctx context.Context) ([]byte, error) {
	statsList, err := s.calculateStats(ctx)
	if err != nil {
		return nil, err
	}

	f := excelize.NewFile()
	defer f.Close() //nolint:errcheck // in-memory file, nothing to flush

	sheet := "Leaderboard"
	if _, err := f.NewSheet(sheet); err != nil {
		return nil, fmt.Errorf("failed to create sheet: %w", err)
	}
	if err := f.DeleteSheet("Sheet1"); err != nil {
		return nil, fmt.Errorf("failed to drop default sheet: %w", err)
	}

	// Cell writes are collected through setCell: a failure used to be discarded
	// per call, so a broken report came back as a plausible-looking file with
	// missing rows.
	var writeErr error
	setCell := func(axis string, value interface{}) {
		if writeErr != nil {
			return
		}
		if err := f.SetCellValue(sheet, axis, value); err != nil {
			writeErr = fmt.Errorf("failed to write cell %s: %w", axis, err)
		}
	}

	headers := []string{"ID", "Player", "Matches", "Wins", "Losses", "WinRate %", "KDA"}
	for i, h := range headers {
		cell, err := excelize.CoordinatesToCellName(i+1, 1)
		if err != nil {
			return nil, fmt.Errorf("failed to build header cell: %w", err)
		}
		setCell(cell, h)
	}

	row := 2
	for _, st := range statsList {
		winRate := calculateWinRate(st.Wins, st.Matches)
		kdaRatio := calculateKDA(st.Kills, st.Deaths, st.Assists)

		setCell(fmt.Sprintf("A%d", row), st.ID)
		setCell(fmt.Sprintf("B%d", row), st.Name)
		setCell(fmt.Sprintf("C%d", row), st.Matches)
		setCell(fmt.Sprintf("D%d", row), st.Wins)
		setCell(fmt.Sprintf("E%d", row), st.Losses)
		setCell(fmt.Sprintf("F%d", row), fmt.Sprintf("%.1f%%", winRate))
		setCell(fmt.Sprintf("G%d", row), fmt.Sprintf("%.2f", kdaRatio))
		row++
	}
	if writeErr != nil {
		return nil, writeErr
	}

	// Column widths are cosmetic — a failure must not sink the whole report.
	for _, w := range []struct {
		from, to string
		width    float64
	}{{"A", "A", 10}, {"B", "B", 20}, {"C", "G", 12}} {
		if err := f.SetColWidth(sheet, w.from, w.to, w.width); err != nil {
			s.logger.Warn("excel: failed to set width for %s:%s: %v", w.from, w.to, err)
		}
	}

	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
