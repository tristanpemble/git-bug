package execenv

import (
	"fmt"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestIdentityOverridePrecedence(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		envValue  string
		envSet    bool
		want      string
		wantSet   bool
		wantError bool
	}{
		{name: "flag beats environment", args: []string{"--identity", "flag123"}, envValue: "env123", envSet: true, want: "flag123", wantSet: true},
		{name: "environment beats default", envValue: "env123", envSet: true, want: "env123", wantSet: true},
		{name: "repository default", wantSet: false},
		{name: "empty flag", args: []string{"--identity", ""}, envValue: "env123", envSet: true, wantSet: true, wantError: true},
		{name: "empty environment", envSet: true, wantSet: true, wantError: true},
		{name: "malformed flag", args: []string{"--identity", "bad/value"}, wantSet: true, wantError: true},
		{name: "too long", envValue: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", envSet: true, wantSet: true, wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var value string
			cmd := &cobra.Command{Use: "test"}
			cmd.Flags().StringVar(&value, "identity", "", "")
			require.NoError(t, cmd.ParseFlags(tt.args))

			got, set, err := identityOverride(cmd, value, func(string) (string, bool) {
				return tt.envValue, tt.envSet
			})
			require.Equal(t, tt.wantSet, set)
			if tt.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestResolveActingIdentityDoesNotChangeRepositoryDefault(t *testing.T) {
	env := NewTestEnv(t)
	defaultIdentity, err := env.Backend.Identities().New("Default User", "default@example.com")
	require.NoError(t, err)
	require.NoError(t, env.Backend.SetUserIdentity(defaultIdentity))
	overrideIdentity, err := env.Backend.Identities().New("Override User", "override@example.com")
	require.NoError(t, err)

	var value string
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().StringVar(&value, "identity", "", "")
	require.NoError(t, cmd.ParseFlags([]string{"--identity", overrideIdentity.Id().Human()}))
	env.IdentityOverride = value

	actor, err := ResolveActingIdentity(cmd, env)
	require.NoError(t, err)
	require.Equal(t, overrideIdentity.Id(), actor.Id())

	configured, err := env.Backend.GetUserIdentity()
	require.NoError(t, err)
	require.Equal(t, defaultIdentity.Id(), configured.Id())
}

func TestResolveActingIdentityValidation(t *testing.T) {
	env := NewTestEnv(t)

	identities := make(map[byte]string)
	var ambiguous string
	var fullID string
	for i := 0; i < 40 && ambiguous == ""; i++ {
		candidate, err := env.Backend.Identities().New(fmt.Sprintf("User %d", i), fmt.Sprintf("user-%d@example.com", i))
		require.NoError(t, err)
		id := candidate.Id().String()
		if fullID == "" {
			fullID = id
		}
		if _, exists := identities[id[0]]; exists {
			ambiguous = id[:1]
		} else {
			identities[id[0]] = id
		}
	}
	require.NotEmpty(t, ambiguous, "identity IDs should contain a repeated one-character prefix")

	tests := []struct {
		name      string
		value     string
		wantID    string
		wantError string
	}{
		{name: "full ID", value: fullID, wantID: fullID},
		{name: "unknown prefix", value: "unknown", wantError: "resolve acting identity"},
		{name: "ambiguous prefix", value: ambiguous, wantError: "Multiple matching identity"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var value string
			cmd := &cobra.Command{Use: "test"}
			cmd.Flags().StringVar(&value, "identity", "", "")
			require.NoError(t, cmd.ParseFlags([]string{"--identity", tt.value}))
			env.IdentityOverride = value

			actor, err := ResolveActingIdentity(cmd, env)
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantID, actor.Id().String())
		})
	}
}
