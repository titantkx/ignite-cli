package app

import (
	"encoding/gob"
	"net"
	"path"

	hplugin "github.com/hashicorp/go-plugin"

	"github.com/ignite/cli/v28/ignite/pkg/cache"
	"github.com/ignite/cli/v28/ignite/pkg/errors"
	"github.com/ignite/cli/v28/ignite/version"
)

const (
	cacheFileName  = "ignite_app_cache.db"
	cacheNamespace = "app.rpc.context"
)

// Caches configuration for shared app hosts.
// The cached configuration can be used to re-attach to running apps.
// These type of apps must have "shared_host: true" in their manifest.
var storageCache *cache.Cache[hplugin.ReattachConfig]

func init() {
	gob.Register(hplugin.ReattachConfig{})
	gob.Register(&net.UnixAddr{})
}

func writeConfigCache(appPath string, conf hplugin.ReattachConfig) error {
	if appPath == "" {
		return errors.Errorf("provided path is invalid: %s", appPath)
	}
	if conf.Addr == nil {
		return errors.Errorf("app Address info cannot be empty")
	}
	cache, err := newCache()
	if err != nil {
		return err
	}
	return cache.Put(appPath, conf)
}

func readConfigCache(appPath string) (hplugin.ReattachConfig, error) {
	if appPath == "" {
		return hplugin.ReattachConfig{}, errors.Errorf("provided path is invalid: %s", appPath)
	}
	cache, err := newCache()
	if err != nil {
		return hplugin.ReattachConfig{}, err
	}
	return cache.Get(appPath)
}

func checkConfCache(appPath string) bool {
	if appPath == "" {
		return false
	}
	cache, err := newCache()
	if err != nil {
		return false
	}
	_, err = cache.Get(appPath)
	return err == nil
}

func deleteConfCache(appPath string) error {
	if appPath == "" {
		return errors.Errorf("provided path is invalid: %s", appPath)
	}
	cache, err := newCache()
	if err != nil {
		return err
	}
	return cache.Delete(appPath)
}

func newCache() (*cache.Cache[hplugin.ReattachConfig], error) {
	cacheRootDir, err := AppsPath()
	if err != nil {
		return nil, err
	}
	if storageCache == nil {
		storage, err := cache.NewStorage(
			path.Join(cacheRootDir, cacheFileName),
			cache.WithVersion(version.Version),
		)
		if err != nil {
			return nil, err
		}
		c := cache.New[hplugin.ReattachConfig](storage, cacheNamespace)
		storageCache = &c
	}
	return storageCache, nil
}
