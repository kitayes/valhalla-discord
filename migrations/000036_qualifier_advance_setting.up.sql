-- The qualifier-cutoff hotfix kept a qualifier's advance line in
-- telegram_settings as 'qualifier_advance:<tournament id>'. The stage columns
-- replace it; carry it over so a qualifier built under the hotfix still stops
-- at its line, and drop the key so nothing reads two sources.
UPDATE telegram_tournaments t
SET stage = 'qualifier',
    advance_count = s.value::int
FROM telegram_settings s
WHERE s.key = 'qualifier_advance:' || t.id
  AND s.value ~ '^[0-9]+$'
  AND s.value::int > 0
  AND t.advance_count = 0;

DELETE FROM telegram_settings WHERE key LIKE 'qualifier\_advance:%';
