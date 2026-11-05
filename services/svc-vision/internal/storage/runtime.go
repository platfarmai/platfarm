package storage

import (
	"sync"

	"platfarm/svc-vision/internal/config"
)

type RuntimeStorage struct {
	mu     sync.RWMutex
	local  config.LocalStorageConfig
	webdav config.WebdavConfig
	minio  config.MinIOConfig
}

func NewRuntimeStorage(cfg *config.Config) *RuntimeStorage {
	return &RuntimeStorage{
		local:  cfg.Storage.Local,
		webdav: cfg.Storage.Webdav,
		minio:  cfg.Storage.MinIO,
	}
}

func (r *RuntimeStorage) GetLocal() config.LocalStorageConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.local
}

func (r *RuntimeStorage) GetWebdav() config.WebdavConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.webdav
}

func (r *RuntimeStorage) GetMinIO() config.MinIOConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.minio
}

func (r *RuntimeStorage) SetLocal(l config.LocalStorageConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.local = l
}

func (r *RuntimeStorage) SetWebdav(w config.WebdavConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.webdav = w
}

func (r *RuntimeStorage) SetMinIO(m config.MinIOConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.minio = m
}
