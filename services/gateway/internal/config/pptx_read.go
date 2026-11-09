package config

// PPTXReadRendererConfig validates the shared renderer independently of the
// mutation-QA rollout phase. Disabled write QA must not bypass the read endpoint
// allowlist, URL validation, or resource limits.
func PPTXReadRendererConfig(visual PPTXVisualQAAdapterConfig) (PPTXVisualQAAdapterConfig, error) {
	visual.Phase = "shadow"
	err := normalizePPTXVisualQAConfig(&visual)
	return visual, err
}
