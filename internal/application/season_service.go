package application

import (
	"context"
	"fmt"
	"sort"
	"time"

	"blackwatch/internal/domain"
	"blackwatch/internal/models"
	"blackwatch/internal/repository"
)

// SeasonLeaderEntry combines a player's live MMR with their seasonal scoreboard stats.
type SeasonLeaderEntry struct {
	PlayerID int
	Name     string
	MMR      int
	Matches  int
	Wins     int
	Losses   int
	Kills    int
	Deaths   int
	Assists  int
	MVP      int
	SVP      int
}

// SeasonOverview combines the active season metadata with live standings and match count.
type SeasonOverview struct {
	Season        models.Season
	MatchesPlayed int
	TopPlayers    []SeasonLeaderEntry
	ActivePlayers int
}

// SeasonHallOfFame holds one archived season's podium/standings plus the list of archived seasons.
type SeasonHallOfFame struct {
	Season          models.Season
	Standings       []models.SeasonStanding
	ArchivedSeasons []models.Season
}

// SeasonService manages ranked seasons, Hall of Fame archives, trophies, and MMR resets.
type SeasonService struct {
	logger   Logger
	repo     repository.QuestSeason
	matchSvc MatchService
	nowFn    func() time.Time
}

func NewSeasonService(logger Logger, repo repository.QuestSeason, matchSvc MatchService) *SeasonService {
	return &SeasonService{
		logger:   logger,
		repo:     repo,
		matchSvc: matchSvc,
		nowFn:    time.Now,
	}
}

func (s *SeasonService) now() time.Time {
	if s.nowFn != nil {
		return s.nowFn()
	}
	return time.Now()
}

// buildLiveRankings merges all active players' CurrentMMR with seasonal match stats,
// keeps qualifiers (played >= 1 match or moved from BaseMMR), and sorts by MMR DESC.
func (s *SeasonService) buildLiveRankings(ctx context.Context) ([]SeasonLeaderEntry, error) {
	players, err := s.matchSvc.GetPlayerList(ctx)
	if err != nil {
		return nil, fmt.Errorf("season: load player list: %w", err)
	}
	stats, err := s.matchSvc.GetLeaderboard(ctx, "wins")
	if err != nil {
		return nil, fmt.Errorf("season: load seasonal stats: %w", err)
	}
	return mergeAndSortSeasonEntries(players, stats), nil
}

func mergeAndSortSeasonEntries(players []models.Player, stats []*PlayerStats) []SeasonLeaderEntry {
	statsByID := make(map[int]*PlayerStats, len(stats))
	for _, st := range stats {
		statsByID[st.ID] = st
	}

	out := make([]SeasonLeaderEntry, 0, len(players))
	for _, pl := range players {
		mmr := pl.CurrentMMR
		if mmr <= 0 {
			mmr = domain.BaseMMR
		}
		entry := SeasonLeaderEntry{
			PlayerID: pl.ID,
			Name:     pl.Name,
			MMR:      mmr,
		}
		if st := statsByID[pl.ID]; st != nil {
			entry.Matches = st.Matches
			entry.Wins = st.Wins
			entry.Losses = st.Losses
			entry.Kills = st.Kills
			entry.Deaths = st.Deaths
			entry.Assists = st.Assists
			entry.MVP = st.MVP
			entry.SVP = st.SVP
		}
		if entry.Matches > 0 || entry.MMR != domain.BaseMMR {
			out = append(out, entry)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].MMR != out[j].MMR {
			return out[i].MMR > out[j].MMR
		}
		if out[i].Wins != out[j].Wins {
			return out[i].Wins > out[j].Wins
		}
		if out[i].Matches != out[j].Matches {
			return out[i].Matches > out[j].Matches
		}
		return out[i].PlayerID < out[j].PlayerID
	})
	return out
}

// GetCurrentSeasonOverview loads the active season, its match count, and live top players.
func (s *SeasonService) GetCurrentSeasonOverview(ctx context.Context, topLimit int) (*SeasonOverview, error) {
	if topLimit <= 0 {
		topLimit = 5
	}
	season, err := s.repo.GetActiveSeason(ctx)
	if err != nil {
		return nil, err
	}
	matchesPlayed, err := s.repo.CountMatchesSince(ctx, season.StartedAt)
	if err != nil {
		s.logger.Warn("season: failed to count matches since %v: %v", season.StartedAt, err)
	}

	qualifying, err := s.buildLiveRankings(ctx)
	if err != nil {
		return nil, err
	}
	top := qualifying
	if len(top) > topLimit {
		top = top[:topLimit]
	}

	return &SeasonOverview{
		Season:        *season,
		MatchesPlayed: matchesPlayed,
		TopPlayers:    top,
		ActivePlayers: len(qualifying),
	}, nil
}

// GetHallOfFame returns the requested archived season (or the most recently archived one when number == 0).
func (s *SeasonService) GetHallOfFame(ctx context.Context, seasonNumber, limit int) (*SeasonHallOfFame, error) {
	if limit <= 0 {
		limit = 10
	}
	archived, err := s.repo.ListArchivedSeasons(ctx, 20)
	if err != nil {
		return nil, err
	}
	if len(archived) == 0 {
		return nil, domain.ErrSeasonNotFound
	}

	var target *models.Season
	if seasonNumber > 0 {
		loaded, err := s.repo.GetSeasonByNumber(ctx, seasonNumber)
		if err != nil {
			return nil, err
		}
		if loaded.Status != "ARCHIVED" {
			return nil, domain.ErrSeasonNotFound
		}
		target = loaded
	} else {
		t := archived[0]
		target = &t
	}

	standings, err := s.repo.GetSeasonStandings(ctx, target.ID, limit)
	if err != nil {
		return nil, err
	}

	return &SeasonHallOfFame{
		Season:          *target,
		Standings:       standings,
		ArchivedSeasons: archived,
	}, nil
}

// GetPlayerTrophies returns Top-3 trophies earned by playerID in archived seasons.
func (s *SeasonService) GetPlayerTrophies(ctx context.Context, playerID int) ([]models.PlayerTrophy, error) {
	return s.repo.GetPlayerTrophies(ctx, playerID)
}

// RenameActiveSeason validates and updates the display name of the active season.
func (s *SeasonService) RenameActiveSeason(ctx context.Context, rawName string) (*models.Season, error) {
	name, err := domain.ValidateSeasonName(rawName)
	if err != nil {
		return nil, err
	}
	return s.repo.RenameActiveSeason(ctx, name)
}

// CloseCurrentSeason archives the active season's standings, awards Top-3 ValCoins,
// resets player MMR according to resetMode (default: full reset to 1000 MMR),
// starts the next season, and invalidates the stats cache.
func (s *SeasonService) CloseCurrentSeason(ctx context.Context, nextName, resetMode, closedBy string) (*models.CloseSeasonResult, error) {
	if resetMode == "" {
		resetMode = domain.SeasonResetModeFull
	}
	if !domain.IsValidSeasonResetMode(resetMode) {
		return nil, fmt.Errorf("%w: %s", domain.ErrInvalidSeasonResetMode, resetMode)
	}
	if nextName != "" {
		validated, err := domain.ValidateSeasonName(nextName)
		if err != nil {
			return nil, err
		}
		nextName = validated
	}

	active, err := s.repo.GetActiveSeason(ctx)
	if err != nil {
		return nil, err
	}
	matchesPlayed, err := s.repo.CountMatchesSince(ctx, active.StartedAt)
	if err != nil {
		s.logger.Warn("season: count matches before close: %v", err)
	}

	qualifiers, err := s.buildLiveRankings(ctx)
	if err != nil {
		return nil, err
	}
	standings := make([]models.SeasonStanding, 0, len(qualifiers))
	for idx, p := range qualifiers {
		rank := idx + 1
		deaths := p.Deaths
		if deaths <= 0 {
			deaths = 1
		}
		kda := 0.0
		if p.Matches > 0 {
			kda = float64(p.Kills+p.Assists) / float64(deaths)
		}
		standings = append(standings, models.SeasonStanding{
			Rank:        rank,
			PlayerID:    p.PlayerID,
			PlayerName:  p.Name,
			FinalMMR:    p.MMR,
			Tier:        string(domain.DetermineTier(p.MMR)),
			Matches:     p.Matches,
			Wins:        p.Wins,
			Losses:      p.Losses,
			MVPCount:    p.MVP,
			SVPCount:    p.SVP,
			KDA:         kda,
			RewardCoins: domain.SeasonPodiumReward(rank),
		})
	}

	res, err := s.repo.CloseSeasonAndReset(ctx, standings, matchesPlayed, nextName, resetMode, closedBy, s.now())
	if err != nil {
		return nil, err
	}
	s.matchSvc.InvalidateStatsCache()
	s.logger.Info("season: closed #%d (%q, %d standings, %d MMR resets, mode=%s) -> started #%d (%q)",
		res.ClosedSeason.Number, res.ClosedSeason.Name, len(res.Standings), res.MMRResetCount, resetMode,
		res.NextSeason.Number, res.NextSeason.Name)
	return res, nil
}
