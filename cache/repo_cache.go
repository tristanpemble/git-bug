package cache

import (
	"fmt"
	"strings"
	"sync"

	"github.com/git-bug/git-bug/entities/bug"
	"github.com/git-bug/git-bug/entities/identity"
	"github.com/git-bug/git-bug/entity"
	"github.com/git-bug/git-bug/repository"
)

// The maximum number of bugs loaded in memory. After that, eviction will be done.
const defaultMaxLoadedBugs = 1000

var _ repository.RepoCommon = &RepoCache{}
var _ repository.RepoConfig = &RepoCache{}
var _ repository.RepoKeyring = &RepoCache{}

// cacheMgmt is the expected interface for a sub-cache.
type cacheMgmt interface {
	Typename() string
	Build() <-chan BuildEvent
	SetCacheSize(size int)
	RemoveAll() error
	MergeAll(remote string) <-chan entity.MergeResult
	GetNamespace() string
	RegisterObserver(repoName string, observer Observer)
	UnregisterObserver(observer Observer)
	Close() error
}

// RepoCache is a cache for a Repository. This cache has multiple functions:
//
//  1. After being loaded, a Bug is kept in memory in the cache, allowing for fast
//     access later.
//  2. The cache maintains in memory a pre-digested excerpt for each bug, allowing
//     fast querying of all bugs without having to load them individually.
//  3. The cache guarantees that a single instance of a Bug is loaded at once, avoiding
//     loss of data that we could have with multiple copies in the same process.
//  4. The same way, the cache maintains in memory a single copy of the loaded identities.
//
// Derived cache state is process-local. Git objects and refs are the durable
// source of truth, so independent processes can rebuild and query concurrently.
type RepoCache struct {
	// the underlying repo
	repo repository.ClockedRepo

	// the name of the repository, as defined in the MultiRepoCache
	name string

	// resolvers for all known entities and excerpts
	resolvers entity.Resolvers

	bugs       *RepoCacheBug
	identities *RepoCacheIdentity

	subcaches []cacheMgmt

	// the user identity's id, if known
	muUserIdentity sync.RWMutex
	userIdentityId entity.Id

	// actorId is the identity selected for this cache invocation. Unlike the
	// repository default above, it is never persisted to Git configuration.
	muActor sync.RWMutex
	actorId entity.Id
}

// NewRepoCache create or open a cache on top of a raw repository.
// The caller is expected to read all returned events before the cache is considered
// ready to use.
func NewRepoCache(r repository.ClockedRepo) (*RepoCache, chan BuildEvent) {
	return NewNamedRepoCache(r, defaultRepoName)
}

// NewNamedRepoCache create or open a named cache on top of a raw repository.
// The caller is expected to read all returned events before the cache is considered
// ready to use.
func NewNamedRepoCache(r repository.ClockedRepo, name string) (*RepoCache, chan BuildEvent) {
	c := &RepoCache{
		repo: r,
		name: name,
	}

	c.identities = NewRepoCacheIdentity(r, c.getResolvers, c.GetActor)
	c.subcaches = append(c.subcaches, c.identities)

	c.bugs = NewRepoCacheBug(r, c.getResolvers, c.GetActor)
	c.subcaches = append(c.subcaches, c.bugs)

	c.resolvers = entity.Resolvers{
		&IdentityCache{}:   entity.ResolverFunc[*IdentityCache](c.identities.Resolve),
		&IdentityExcerpt{}: entity.ResolverFunc[*IdentityExcerpt](c.identities.ResolveExcerpt),
		&BugCache{}:        entity.ResolverFunc[*BugCache](c.bugs.Resolve),
		&BugExcerpt{}:      entity.ResolverFunc[*BugExcerpt](c.bugs.ResolveExcerpt),
	}

	// small buffer so that the functions below can emit an event without blocking
	events := make(chan BuildEvent)

	go func() {
		defer close(events)
		c.buildCache(events)
	}()

	return c, events
}

func NewRepoCacheNoEvents(r repository.ClockedRepo) (*RepoCache, error) {
	cache, events := NewRepoCache(r)
	for event := range events {
		if event.Err != nil {
			for range events {
			}
			return nil, event.Err
		}
	}
	return cache, nil
}

// Bugs gives access to the Bug entities
func (c *RepoCache) Bugs() *RepoCacheBug {
	return c.bugs
}

// Identities gives access to the Identity entities
func (c *RepoCache) Identities() *RepoCacheIdentity {
	return c.identities
}

func (c *RepoCache) getResolvers() entity.Resolvers {
	return c.resolvers
}

// setCacheSize change the maximum number of loaded bugs
func (c *RepoCache) setCacheSize(size int) {
	for _, subcache := range c.subcaches {
		subcache.SetCacheSize(size)
	}
}

func (c *RepoCache) Close() error {
	for _, mgmt := range c.subcaches {
		if err := mgmt.Close(); err != nil {
			return err
		}
	}
	return c.repo.Close()
}

func (c *RepoCache) buildCache(events chan BuildEvent) {
	events <- BuildEvent{Event: BuildEventCacheIsBuilt}

	for _, subcache := range c.subcaches {
		buildEvents := subcache.Build()
		for buildEvent := range buildEvents {
			events <- buildEvent
			if buildEvent.Err != nil {
				return
			}
		}
	}
}

func (c *RepoCache) registerObserver(repoName string, typename string, observer Observer) error {
	switch typename {
	case bug.Typename:
		c.bugs.RegisterObserver(repoName, observer)
	case identity.Typename:
		c.identities.RegisterObserver(repoName, observer)
	default:
		var allTypenames []string
		for _, subcache := range c.subcaches {
			allTypenames = append(allTypenames, subcache.Typename())
		}
		return fmt.Errorf("unknown typename `%s`, available types are [%s]", typename, strings.Join(allTypenames, ", "))
	}
	return nil
}

func (c *RepoCache) registerAllObservers(repoName string, observer Observer) {
	for _, subcache := range c.subcaches {
		subcache.RegisterObserver(repoName, observer)
	}
}

func (c *RepoCache) unregisterAllObservers(observer Observer) {
	for _, subcache := range c.subcaches {
		subcache.UnregisterObserver(observer)
	}
}
