package storage

import (
	"github.com/hashicorp/go-hclog"
	"github.com/radekg/boos/pkg/storage/memory"
	"github.com/radekg/boos/pkg/storage/types"
)

func GetStorage(kind string, settings map[string]interface{}, logger hclog.Logger) (types.Backend, error) {
	var impl types.Backend
	switch kind {
	case "memory":
		impl = memory.New()
	}
	if err := impl.Configure(settings, logger.Named(kind)); err != nil {
		return nil, err
	}
	return impl, nil
}
