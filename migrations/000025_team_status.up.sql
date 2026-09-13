-- Technical defeat used to be a broadcast and nothing else: the sweep messaged
-- the captains and the admins and forgot. The list of who is out lived in a
-- chat, /list_teams and /export kept showing the team as if nothing happened,
-- and a captain who was a minute late could not be put back by anyone.
ALTER TABLE telegram_teams
    ADD COLUMN status VARCHAR(16) NOT NULL DEFAULT 'active';
