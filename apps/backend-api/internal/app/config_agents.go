package app

// AgentProtocolConfig uses the same file-backed secret resolver and rotating
// keyring mechanism as IAM. Empty keys disable credential writes only.
type AgentProtocolConfig struct {
	EncryptionKey              string   `mapstructure:"encryption_key" default:"" secret:"true" description:"base64 key sealing team tool credentials"`
	EncryptionKeyFile          string   `mapstructure:"encryption_key_file" default:""`
	PreviousEncryptionKeys     []string `mapstructure:"previous_encryption_keys" secret:"true"`
	PreviousEncryptionKeyFiles []string `mapstructure:"previous_encryption_key_files"`
}
