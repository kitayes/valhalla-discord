package application

func calculateWinRate(wins, matches int) float64 {
	if matches == 0 {
		return 0.0
	}
	return (float64(wins) / float64(matches)) * 100
}

func calculateKDA(kills, deaths, assists int) float64 {
	d := deaths
	if d == 0 {
		d = minDeathsForKDA
	}
	return float64(kills+assists) / float64(d)
}

func comparePlayersByPriority(p1, p2 *PlayerStats) bool {
	if p1.Matches != p2.Matches {
		return p1.Matches > p2.Matches
	}

	// Then by win rate
	wr1 := calculateWinRate(p1.Wins, p1.Matches)
	wr2 := calculateWinRate(p2.Wins, p2.Matches)
	if wr1 != wr2 {
		return wr1 > wr2
	}

	// Finally by KDA
	kda1 := calculateKDA(p1.Kills, p1.Deaths, p1.Assists)
	kda2 := calculateKDA(p2.Kills, p2.Deaths, p2.Assists)
	return kda1 > kda2
}
