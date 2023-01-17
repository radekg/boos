package memory

import (
	"fmt"
	"sync"

	"github.com/hashicorp/go-hclog"
	"github.com/radekg/boos/pkg/storage/types"
)

type storageBackend struct {
	logger  hclog.Logger
	buckets map[string]*mediaBucket
	lock    *sync.RWMutex
}

// New creates a new storage backend instance.
func New() types.Backend {
	return &storageBackend{
		buckets: map[string]*mediaBucket{},
		lock:    &sync.RWMutex{},
	}
}

// Configure configures the storage backend.
func (b *storageBackend) Configure(settings map[string]interface{}, logger hclog.Logger) error {
	b.logger = logger
	return nil
}

func (impl *storageBackend) ContainsBucket(key string) bool {
	impl.lock.RLock()
	defer impl.lock.RUnlock()
	_, ok := impl.buckets[key]
	return ok
}

func (impl *storageBackend) MediaContainerReaderForBucket(key string) (types.MediaContainerReader, error) {
	impl.lock.Lock()
	defer impl.lock.Unlock()
	bucket, ok := impl.buckets[key]
	if !ok {
		return nil, fmt.Errorf("bucket not found: key %s", key)
	}
	return &mediaContainerReader{bucket: bucket, logger: impl.logger.Named("media-writer")}, nil
}

func (impl *storageBackend) MediaContainerWriterForBucket(key string) (types.MediaContainerWriter, error) {
	impl.lock.Lock()
	defer impl.lock.Unlock()
	if _, ok := impl.buckets[key]; !ok {
		impl.buckets[key] = &mediaBucket{
			containers: []*mediaContainer{},
		}
	}
	newMediaContainer := &mediaContainer{}
	//bucketIndex := len(impl.buckets[key].containers)
	impl.buckets[key].containers = append(impl.buckets[key].containers, newMediaContainer)
	return &mediaContainerWriter{container: newMediaContainer, logger: impl.logger.Named("media-writer")}, nil
}
