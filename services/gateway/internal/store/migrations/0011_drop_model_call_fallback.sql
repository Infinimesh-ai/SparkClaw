-- The implicit Fast->Deep fallback was removed with the capacity contract; no
-- producer has written model_calls.fallback since, so the column only
-- restated a constant false.
ALTER TABLE model_calls DROP COLUMN IF EXISTS fallback;
