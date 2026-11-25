package discord

import (
	"testing"
	"time"

	"blackwatch/internal/domain"
	"blackwatch/internal/models"

	"github.com/bwmarrin/discordgo"
)

func TestActivityTrackerVoiceEligibility(t *testing.T) {
	tr := newActivityTracker()
	tr.setGuildSnapshot("g1", "afk-ch", []*discordgo.VoiceState{
		// Solo user in ch-solo -> ineligible
		{GuildID: "g1", ChannelID: "ch-solo", UserID: "u-solo"},
		// Two users in AFK channel -> ineligible
		{GuildID: "g1", ChannelID: "afk-ch", UserID: "u-afk-1"},
		{GuildID: "g1", ChannelID: "afk-ch", UserID: "u-afk-2"},
		// One human + one bot in ch-bot -> only 1 human, ineligible
		{GuildID: "g1", ChannelID: "ch-bot", UserID: "u-human"},
		{GuildID: "g1", ChannelID: "ch-bot", UserID: "u-bot"},
		// Active channel: 1 unmuted, 1 muted, 1 deafened -> 2 eligible (unmuted + muted)
		{GuildID: "g1", ChannelID: "ch-active", UserID: "u-talk"},
		{GuildID: "g1", ChannelID: "ch-active", UserID: "u-listen", SelfMute: true},
		{GuildID: "g1", ChannelID: "ch-active", UserID: "u-deaf", SelfDeaf: true},
	}, func(uid string) bool { return uid == "u-bot" })

	got := tr.collectEligibleVoiceUsers()
	if len(got) != 2 {
		t.Fatalf("eligible voice users = %+v, want 2", got)
	}

	byID := make(map[string]bool, len(got))
	for _, u := range got {
		byID[u.UserID] = u.Muted
	}
	if muted, ok := byID["u-talk"]; !ok || muted {
		t.Errorf("u-talk = (ok=%v, muted=%v), want (true, false)", ok, muted)
	}
	if muted, ok := byID["u-listen"]; !ok || !muted {
		t.Errorf("u-listen = (ok=%v, muted=%v), want (true, true)", ok, muted)
	}

	// If u-listen leaves, u-talk is alone -> 0 eligible
	tr.updateVoiceState(&discordgo.VoiceState{GuildID: "g1", ChannelID: "", UserID: "u-listen"}, false)
	if gotAfter := tr.collectEligibleVoiceUsers(); len(gotAfter) != 0 {
		t.Fatalf("after partner left, eligible = %+v, want 0", gotAfter)
	}
}

func TestActivityTrackerChatCooldownBufferedMsgsAndDailyCaps(t *testing.T) {
	tr := newActivityTracker()
	t0 := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)

	// First message grants XP and flushes 1 message.
	xp, msgs := tr.recordChatMessage("u1", 5, t0)
	if xp != 5 || msgs != 1 {
		t.Fatalf("msg #1 = (%d, %d), want (5, 1)", xp, msgs)
	}
	// Two quick messages inside 15s cooldown: 0 XP now, but buffered for Noctaly-style count!
	xp, msgs = tr.recordChatMessage("u1", 5, t0.Add(5*time.Second))
	if xp != 0 || msgs != 0 {
		t.Fatalf("msg #2 in cooldown = (%d, %d), want (0, 0)", xp, msgs)
	}
	xp, msgs = tr.recordChatMessage("u1", 5, t0.Add(10*time.Second))
	if xp != 0 || msgs != 0 {
		t.Fatalf("msg #3 in cooldown = (%d, %d), want (0, 0)", xp, msgs)
	}
	// Another user is not blocked by u1 cooldown.
	if u2XP, u2Msgs := tr.recordChatMessage("u2", 8, t0.Add(3*time.Second)); u2XP != 8 || u2Msgs != 1 {
		t.Fatalf("u2 msg = (%d, %d), want (8, 1)", u2XP, u2Msgs)
	}
	// After 15s cooldown passes, u1 gets XP AND flushes all 3 messages (2 buffered + this one)!
	xp, msgs = tr.recordChatMessage("u1", 8, t0.Add(domain.ChatXPCooldown))
	if xp != 8 || msgs != 3 {
		t.Fatalf("msg #4 after cooldown = (%d, %d), want (8, 3)", xp, msgs)
	}

	// Daily chat XP cap enforcement + next-day reset.
	trCap := newActivityTracker()
	var total int64
	for i := 0; i < 120; i++ {
		got, _ := trCap.recordChatMessage("chatter", 8, t0.Add(time.Duration(i)*domain.ChatXPCooldown))
		total += got
	}
	if total != domain.ChatDailyXPCap {
		t.Fatalf("daily chat XP total = %d, want cap %d", total, domain.ChatDailyXPCap)
	}
	// Messages sent after cap are still buffered for periodic flush!
	pending := trCap.drainPendingMessages()
	if pending["chatter"] <= 0 {
		t.Fatalf("expected post-cap messages to be buffered, got %+v", pending)
	}
	// Next UTC day resets the chat XP cap.
	if nextDayXP, _ := trCap.recordChatMessage("chatter", 8, t0.Add(24*time.Hour)); nextDayXP != 8 {
		t.Fatalf("next day chat XP = %d, want 8", nextDayXP)
	}

	// Daily voice XP cap enforcement.
	var voiceTotal int64
	for i := 0; i < 200; i++ {
		voiceTotal += trCap.claimVoiceXP("voicer", domain.VoiceXPPerMinUnmuted, t0)
	}
	if voiceTotal != domain.VoiceDailyXPCap {
		t.Fatalf("daily voice XP total = %d, want cap %d", voiceTotal, domain.VoiceDailyXPCap)
	}
}

func TestParseDiscordBetStake(t *testing.T) {
	allowed := []int{10, 25, 50}

	team, stake, isMax, insured, matchID, ok := parseDiscordBetStake("dbet_stake_a_25_0_42", allowed)
	if !ok || team != domain.TeamA || stake != 25 || isMax || insured || matchID != 42 {
		t.Fatalf("parse fixed stake = (%q,%d,%v,%v,%d,%v)", team, stake, isMax, insured, matchID, ok)
	}

	team, stake, isMax, insured, matchID, ok = parseDiscordBetStake("dbet_stake_b_max_1_7", allowed)
	if !ok || team != domain.TeamB || stake != 0 || !isMax || !insured || matchID != 7 {
		t.Fatalf("parse max insured = (%q,%d,%v,%v,%d,%v)", team, stake, isMax, insured, matchID, ok)
	}

	for _, bad := range []string{
		"dbet_stake_a_999_0_42", // unlisted stake
		"dbet_stake_c_10_0_42",  // invalid team
		"dbet_stake_a_10_2_42",  // invalid insured flag
		"dbet_stake_a_10_0_0",   // invalid match id
		"dbet_stake_a_10_0",     // missing part
	} {
		if _, _, _, _, _, ok := parseDiscordBetStake(bad, allowed); ok {
			t.Errorf("expected %q to be rejected", bad)
		}
	}
}

func TestMatchResultButtonsWithAndWithoutDedicatedBettingChannel(t *testing.T) {
	// Dedicated BETTING_CHANNEL_ID set -> match card has ONLY referee WIN buttons.
	onlyWin := matchResultButtons(15, false)
	if len(onlyWin) != 1 {
		t.Fatalf("expected 1 row when BETTING_CHANNEL_ID is set, got %d", len(onlyWin))
	}

	// Fallback when BETTING_CHANNEL_ID is empty -> match card carries betting row too.
	withBet := matchResultButtons(15, true)
	if len(withBet) != 2 {
		t.Fatalf("expected 2 component rows (WIN + betting), got %d", len(withBet))
	}
	betRow, ok := withBet[1].(discordgo.ActionsRow)
	if !ok || len(betRow.Components) != 3 {
		t.Fatalf("expected 3 betting buttons in second row, got %#v", withBet[1])
	}
	wantIDs := []string{"dbet_open_a_15", "dbet_open_b_15", "dbet_pool_15"}
	for idx, want := range wantIDs {
		btn, ok := betRow.Components[idx].(discordgo.Button)
		if !ok || btn.CustomID != want {
			t.Errorf("bet button[%d] = %#v, want CustomID %q", idx, betRow.Components[idx], want)
		}
	}
}

func TestShopShowcaseViewIncludesBalanceAndRefreshButtons(t *testing.T) {
	items := []models.ShopItem{
		{ID: 1, Code: "custom_title", Name: "Титул", Kind: domain.ShopKindTitle, Price: 250, Enabled: true},
	}
	embed, comps := buildShopCatalogView(items, "Header\n", true)
	if embed == nil || len(comps) != 2 {
		t.Fatalf("showcase view: embed=%v len(comps)=%d, want 2 rows", embed != nil, len(comps))
	}
	btnRow, ok := comps[1].(discordgo.ActionsRow)
	if !ok || len(btnRow.Components) != 2 {
		t.Fatalf("expected 2 showcase buttons, got %#v", comps[1])
	}
	b0, _ := btnRow.Components[0].(discordgo.Button)
	b1, _ := btnRow.Components[1].(discordgo.Button)
	if b0.CustomID != buttonShopMyBalance || b1.CustomID != buttonShopRefresh {
		t.Errorf("showcase button IDs = (%q, %q)", b0.CustomID, b1.CustomID)
	}
}

func TestEconomyCommandsRegistered(t *testing.T) {
	cmds := commandsByName(t, initBot(t))
	for _, name := range []string{
		"wallet", "daily", "tip", "rich", "shop",
		"coins_admin", "shop_admin", "xp_admin", "profile_admin",
		"revert_match", "queue_unban",
	} {
		if _, ok := cmds[name]; !ok {
			t.Errorf("command /%s is not registered", name)
		}
	}
	wantShopSubs := map[string]bool{"add": false, "toggle": false, "list": false, "post": false, "orders": false, "refund": false, "delete": false}
	for _, opt := range cmds["shop_admin"].Options {
		if _, ok := wantShopSubs[opt.Name]; ok {
			wantShopSubs[opt.Name] = true
		}
	}
	for sub, found := range wantShopSubs {
		if !found {
			t.Errorf("/shop_admin is missing %q subcommand", sub)
		}
	}
}
