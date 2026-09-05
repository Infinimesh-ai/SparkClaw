CREATE INDEX IF NOT EXISTS idx_model_calls_lane_latest
  ON model_calls(lane, started_at DESC, id DESC);
