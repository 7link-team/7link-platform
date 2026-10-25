package app

import "7link.com/platform/internal/fleet"

// FleetGatewayConfig configures sealing independently from IAM credentials.
type FleetGatewayConfig struct {
	Preview                       fleet.PreviewConfig `mapstructure:"preview" group:"preview"`
	GatewayEncryptionKey          string              `mapstructure:"gateway_encryption_key" secret:"true"`
	GatewayEncryptionKeyFile      string              `mapstructure:"gateway_encryption_key_file"`
	PreviousGatewayEncryptionKeys []string            `mapstructure:"previous_gateway_encryption_keys" secret:"true"`
}
