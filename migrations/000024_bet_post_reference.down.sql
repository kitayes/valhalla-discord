ALTER TABLE lobby_matches
    DROP COLUMN IF EXISTS tg_bet_chat_id,
    DROP COLUMN IF EXISTS tg_bet_message_id;
