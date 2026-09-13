-- The Telegram post that carries the betting keyboard is edited after every bet
-- to show the current pool and coefficients, and edited once more when the
-- window closes so its buttons stop inviting taps that can only be refused.
--
-- Editing needs the chat and message the post lives in. Keeping that only in
-- memory would lose it on restart: the match keeps taking bets, but its post
-- freezes on whatever numbers it happened to show, and after the window expires
-- it keeps advertising a market that is already closed.
--
-- Nullable: a match created while Telegram is unconfigured has no post at all.
ALTER TABLE lobby_matches
    ADD COLUMN IF NOT EXISTS tg_bet_chat_id BIGINT,
    -- BIGINT rather than INT: message ids are documented as fitting a 32-bit
    -- integer today, with no promise they always will.
    ADD COLUMN IF NOT EXISTS tg_bet_message_id BIGINT;
