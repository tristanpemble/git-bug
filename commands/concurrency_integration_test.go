package commands

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/git-bug/git-bug/cache"
	"github.com/git-bug/git-bug/entities/identity"
	"github.com/git-bug/git-bug/entity"
	"github.com/git-bug/git-bug/repository"
)

const commandHelperEnvironment = "GIT_BUG_COMMAND_HELPER"

func TestCommandHelperProcess(t *testing.T) {
	if os.Getenv(commandHelperEnvironment) != "1" {
		return
	}

	args := helperArgs(os.Args)
	if len(args) > 0 && args[0] == "__hold_cache" {
		holdCacheForTest(args[1:])
		return
	}

	root := NewRootCommand(context.Background(), "test")
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestConcurrentCommandsPreserveIdentityAndDurableOperations(t *testing.T) {
	dir, defaultID, overrideID, independentBugIDs, contestedBugID := newConcurrentCommandRepo(t)

	// Read-only commands build independent caches and do not exclude each other.
	reads := []*exec.Cmd{
		newHelperCommand(t, dir, nil, "bug", "--format", "json"),
		newHelperCommand(t, dir, nil, "bug", "--format", "json"),
		newHelperCommand(t, dir, nil, "bug", "--format", "json"),
	}
	for _, cmd := range reads {
		require.NoError(t, cmd.Start())
	}
	for _, cmd := range reads {
		require.NoError(t, cmd.Wait())
	}

	// Environment and command-line identities write distinct issues concurrently.
	envWrite := newHelperCommand(t, dir, map[string]string{
		"GIT_BUG_IDENTITY": defaultID.String(),
	}, "bug", "comment", "new", independentBugIDs[0].String(), "--message", "environment actor", "--non-interactive")
	flagWrite := newHelperCommand(t, dir, map[string]string{
		"GIT_BUG_IDENTITY": defaultID.String(),
	}, "--identity", overrideID.Human(), "bug", "comment", "new", independentBugIDs[1].String(), "--message", "flag actor", "--non-interactive")
	require.NoError(t, envWrite.Start())
	require.NoError(t, flagWrite.Start())
	require.NoError(t, envWrite.Wait(), commandOutput(envWrite))
	require.NoError(t, flagWrite.Wait(), commandOutput(flagWrite))

	assertCommentAuthor(t, dir, independentBugIDs[0], "environment actor", defaultID)
	assertCommentAuthor(t, dir, independentBugIDs[1], "flag actor", overrideID)
	assertConfiguredIdentity(t, dir, defaultID)

	// Competing writes to one issue either serialize or return the explicit
	// retryable reference conflict. In both cases every successful operation is durable.
	first := newHelperCommand(t, dir, map[string]string{
		"GIT_BUG_IDENTITY": defaultID.String(),
	}, "bug", "comment", "new", contestedBugID.String(), "--message", "contender one", "--non-interactive")
	second := newHelperCommand(t, dir, map[string]string{
		"GIT_BUG_IDENTITY": overrideID.String(),
	}, "bug", "comment", "new", contestedBugID.String(), "--message", "contender two", "--non-interactive")
	require.NoError(t, first.Start())
	require.NoError(t, second.Start())
	firstErr := first.Wait()
	secondErr := second.Wait()
	require.True(t, firstErr == nil || secondErr == nil, "both competing writes failed: %s / %s", commandOutput(first), commandOutput(second))
	if firstErr != nil {
		require.Contains(t, commandOutput(first), repository.ErrReferenceConflict.Error())
	}
	if secondErr != nil {
		require.Contains(t, commandOutput(second), repository.ErrReferenceConflict.Error())
	}
	if firstErr == nil {
		assertCommentAuthor(t, dir, contestedBugID, "contender one", defaultID)
	}
	if secondErr == nil {
		assertCommentAuthor(t, dir, contestedBugID, "contender two", overrideID)
	}

	// Unknown identities fail before an operation is added.
	before := operationCount(t, dir, contestedBugID)
	invalid := newHelperCommand(t, dir, map[string]string{
		"GIT_BUG_IDENTITY": "unknown",
	}, "bug", "comment", "new", contestedBugID.String(), "--message", "must not be written", "--non-interactive")
	err := invalid.Run()
	require.Error(t, err)
	require.Contains(t, commandOutput(invalid), "resolve acting identity")
	require.Equal(t, before, operationCount(t, dir, contestedBugID))

	// A killed cache holder and an obsolete upstream lock file cannot make the
	// repository unavailable to the next process.
	ready := filepath.Join(t.TempDir(), "ready")
	holder := newHelperCommand(t, dir, nil, "__hold_cache", ready)
	require.NoError(t, holder.Start())
	require.Eventually(t, func() bool {
		_, err := os.Stat(ready)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, holder.Process.Kill())
	_ = holder.Wait()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git", "git-bug", "lock"), []byte("999999"), 0600))
	afterCrash := newHelperCommand(t, dir, nil, "bug", "--format", "json")
	require.NoError(t, afterCrash.Run(), commandOutput(afterCrash))

	// Read commands remain available after the human default is cleared.
	repo, err := repository.OpenGoGitRepo(dir, "git-bug", nil)
	require.NoError(t, err)
	require.NoError(t, identity.ClearUserIdentity(repo))
	require.NoError(t, repo.Close())
	withoutIdentity := newHelperCommand(t, dir, map[string]string{
		"GIT_BUG_IDENTITY": "",
	}, "bug", "--format", "json")
	require.NoError(t, withoutIdentity.Run(), commandOutput(withoutIdentity))

	// A mutation without any acting identity fails before adding an operation.
	before = operationCount(t, dir, contestedBugID)
	missingIdentity := newHelperCommand(t, dir, nil,
		"bug", "comment", "new", contestedBugID.String(), "--message", "must not be written", "--non-interactive")
	err = missingIdentity.Run()
	require.Error(t, err)
	require.Contains(t, commandOutput(missingIdentity), "No identity is set")
	require.Equal(t, before, operationCount(t, dir, contestedBugID))
}

func newConcurrentCommandRepo(t *testing.T) (string, entity.Id, entity.Id, [2]entity.Id, entity.Id) {
	t.Helper()
	dir := t.TempDir()
	repo, err := repository.InitGoGitRepo(dir, "git-bug")
	require.NoError(t, err)
	require.NoError(t, repo.LocalConfig().StoreString("user.name", "test user"))
	require.NoError(t, repo.LocalConfig().StoreString("user.email", "test@example.com"))
	backend, err := cache.NewRepoCacheNoEvents(repo)
	require.NoError(t, err)

	defaultIdentity, err := backend.Identities().New("Default Actor", "default@example.com")
	require.NoError(t, err)
	overrideIdentity, err := backend.Identities().New("Override Actor", "override@example.com")
	require.NoError(t, err)
	require.NoError(t, backend.SetUserIdentity(defaultIdentity))

	var independent [2]entity.Id
	for i := range independent {
		b, _, err := backend.Bugs().New(fmt.Sprintf("independent %d", i), "body")
		require.NoError(t, err)
		independent[i] = b.Id()
	}
	contested, _, err := backend.Bugs().New("contested", "body")
	require.NoError(t, err)
	require.NoError(t, backend.Close())

	return dir, defaultIdentity.Id(), overrideIdentity.Id(), independent, contested.Id()
}

func newHelperCommand(t *testing.T, dir string, values map[string]string, args ...string) *exec.Cmd {
	t.Helper()
	commandArgs := []string{"-test.run=^TestCommandHelperProcess$", "--"}
	commandArgs = append(commandArgs, args...)
	cmd := exec.Command(os.Args[0], commandArgs...)
	cmd.Dir = dir
	cmd.Env = helperEnvironment(values)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	return cmd
}

func helperEnvironment(values map[string]string) []string {
	env := make([]string, 0, len(os.Environ())+len(values)+1)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, commandHelperEnvironment+"=") || strings.HasPrefix(entry, "GIT_BUG_IDENTITY=") {
			continue
		}
		env = append(env, entry)
	}
	env = append(env, commandHelperEnvironment+"=1")
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	return env
}

func helperArgs(args []string) []string {
	for i, arg := range args {
		if arg == "--" {
			return args[i+1:]
		}
	}
	return nil
}

func holdCacheForTest(args []string) {
	if len(args) != 1 {
		os.Exit(2)
	}
	repo, err := repository.OpenGoGitRepo(".", "git-bug", nil)
	if err != nil {
		os.Exit(2)
	}
	_, events := cache.NewRepoCache(repo)
	for event := range events {
		if event.Err != nil {
			os.Exit(2)
		}
	}
	if err := os.WriteFile(args[0], []byte("ready"), 0600); err != nil {
		os.Exit(2)
	}
	select {}
}

func assertCommentAuthor(t *testing.T, dir string, bugID entity.Id, message string, authorID entity.Id) {
	t.Helper()
	backend := openBackendForTest(t, dir)
	b, err := backend.Bugs().Resolve(bugID)
	require.NoError(t, err)
	for _, comment := range b.Snapshot().Comments {
		if comment.Message == message {
			require.Equal(t, authorID, comment.Author.Id())
			return
		}
	}
	t.Fatalf("comment %q not found", message)
}

func assertConfiguredIdentity(t *testing.T, dir string, want entity.Id) {
	t.Helper()
	repo, err := repository.OpenGoGitRepo(dir, "git-bug", nil)
	require.NoError(t, err)
	got, err := identity.GetUserIdentityId(repo)
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.NoError(t, repo.Close())
}

func operationCount(t *testing.T, dir string, bugID entity.Id) int {
	t.Helper()
	backend := openBackendForTest(t, dir)
	b, err := backend.Bugs().Resolve(bugID)
	require.NoError(t, err)
	return len(b.Snapshot().AllOperations())
}

func openBackendForTest(t *testing.T, dir string) *cache.RepoCache {
	t.Helper()
	repo, err := repository.OpenGoGitRepo(dir, "git-bug", nil)
	require.NoError(t, err)
	backend, err := cache.NewRepoCacheNoEvents(repo)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, backend.Close())
	})
	return backend
}

func commandOutput(cmd *exec.Cmd) string {
	if output, ok := cmd.Stdout.(*bytes.Buffer); ok {
		return output.String()
	}
	return ""
}
