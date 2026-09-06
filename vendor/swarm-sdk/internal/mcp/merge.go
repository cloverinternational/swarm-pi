package mcp

func mergeServer(base ServerConfig, patch ServerPatch) ServerConfig {
	merged := base
	if patch.Type != nil {
		merged.Type = *patch.Type
	}
	if patch.Enabled != nil {
		merged.Enabled = *patch.Enabled
	}
	if patch.CredentialRef != nil {
		merged.CredentialRef = *patch.CredentialRef
	}
	if patch.Command != nil {
		merged.Command = *patch.Command
	}
	if patch.Args != nil {
		merged.Args = copyStringSlice(*patch.Args)
	}
	if patch.Env != nil {
		merged.Env = copyStringMap(*patch.Env)
	}
	if patch.SecretEnv != nil {
		merged.SecretEnv = copyStringSlice(*patch.SecretEnv)
	}
	if patch.WorkingDir != nil {
		merged.WorkingDir = *patch.WorkingDir
	}
	if patch.URL != nil {
		merged.URL = *patch.URL
	}
	if patch.Headers != nil {
		merged.Headers = copyStringMap(*patch.Headers)
	}
	if patch.SecretHeaders != nil {
		merged.SecretHeaders = copyStringSlice(*patch.SecretHeaders)
	}
	if patch.Auth != nil {
		merged.Auth = patch.Auth
	}
	if patch.TimeoutSec != nil {
		merged.TimeoutSec = *patch.TimeoutSec
	}
	if patch.Retries != nil {
		merged.Retries = *patch.Retries
	}
	if patch.OAuth != nil {
		merged.OAuth = patch.OAuth
	}
	if patch.Tools != nil {
		merged.Tools = patch.Tools
	}
	return merged
}

// MergeServer applies a patch to a base server config.
func MergeServer(base ServerConfig, patch ServerPatch) ServerConfig {
	return mergeServer(base, patch)
}
