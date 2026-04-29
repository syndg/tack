package promotions

type ThresholdConfig struct {
	SupportThreshold    int
	AutoApprovalEnabled bool
}

type ThresholdMetadata struct {
	SupportCount        int     `json:"support_count"`
	Confidence          float64 `json:"confidence"`
	ThresholdEligible   bool    `json:"threshold_eligible"`
	AutoApprovalEnabled bool    `json:"auto_approval_enabled"`
	AutoApproved        bool    `json:"auto_approved"`
}

func DefaultThresholdConfig() ThresholdConfig {
	return ThresholdConfig{SupportThreshold: 3}
}

func EvaluateThreshold(support int, cfg ThresholdConfig) ThresholdMetadata {
	if cfg.SupportThreshold <= 0 {
		cfg.SupportThreshold = DefaultThresholdConfig().SupportThreshold
	}
	eligible := support >= cfg.SupportThreshold
	return ThresholdMetadata{
		SupportCount:        support,
		Confidence:          confidenceForSupport(support),
		ThresholdEligible:   eligible,
		AutoApprovalEnabled: cfg.AutoApprovalEnabled,
		AutoApproved:        cfg.AutoApprovalEnabled && eligible,
	}
}
