package execenv

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/git-bug/git-bug/cache"
)

const IdentityEnvironmentVariable = "GIT_BUG_IDENTITY"

// ResolveActingIdentity applies the command-line, environment, repository
// precedence contract and resolves the result without changing Git config.
func ResolveActingIdentity(cmd *cobra.Command, env *Env) (*cache.IdentityCache, error) {
	actor, overridden, err := ResolveOptionalActingIdentity(cmd, env)
	if err != nil {
		return nil, err
	}
	if overridden {
		return actor, nil
	}
	return env.Backend.GetUserIdentity()
}

// ResolveOptionalActingIdentity resolves an explicit command-line or
// environment override. It does not fall back to the repository default.
func ResolveOptionalActingIdentity(cmd *cobra.Command, env *Env) (*cache.IdentityCache, bool, error) {
	value, overridden, err := identityOverride(cmd, env.IdentityOverride, os.LookupEnv)
	if err != nil || !overridden {
		return nil, overridden, err
	}
	actor, err := env.Backend.Identities().ResolvePrefix(value)
	if err != nil {
		return nil, true, fmt.Errorf("resolve acting identity %q: %w", value, err)
	}
	return actor, true, nil
}

func identityOverride(cmd *cobra.Command, flagValue string, lookupEnv func(string) (string, bool)) (string, bool, error) {
	if flag := cmd.Flag("identity"); flag != nil && flag.Changed {
		if err := validateIdentityPrefix(flagValue); err != nil {
			return "", true, fmt.Errorf("invalid --identity value: %w", err)
		}
		return flagValue, true, nil
	}

	if value, ok := lookupEnv(IdentityEnvironmentVariable); ok {
		if err := validateIdentityPrefix(value); err != nil {
			return "", true, fmt.Errorf("invalid %s value: %w", IdentityEnvironmentVariable, err)
		}
		return value, true, nil
	}

	return "", false, nil
}

func validateIdentityPrefix(value string) error {
	if value == "" {
		return fmt.Errorf("value is empty")
	}
	if len(value) > 64 {
		return fmt.Errorf("value is longer than a full identity ID")
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("value contains surrounding whitespace")
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return fmt.Errorf("value contains invalid character %q", r)
		}
	}
	return nil
}
