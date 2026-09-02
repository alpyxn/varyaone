package storage

import (
	"context"
	"fmt"
	"io"
	"strings"
)

type ProviderKind string

const (
	ProviderLocal ProviderKind = "local"
	ProviderS3    ProviderKind = "s3"
	ProviderMinIO ProviderKind = "minio"
)

// Config is the stable configuration contract shared by local, S3-compatible
// and MinIO deployments. Secret values are read by the application wiring and
// are never copied into ObjectInfo or logs.
type Config struct {
	Provider     ProviderKind
	LocalRoot    string
	Endpoint     string
	Bucket       string
	Region       string
	AccessKey    string
	SecretKey    string
	UsePathStyle bool
}

func (c Config) Validate() error {
	switch ProviderKind(strings.ToLower(strings.TrimSpace(string(c.Provider)))) {
	case "", ProviderLocal:
		if strings.TrimSpace(c.LocalRoot) == "" {
			return fmt.Errorf("%w: local_root is required", ErrProviderNotConfigured)
		}
	case ProviderS3, ProviderMinIO:
		if strings.TrimSpace(c.Endpoint) == "" || strings.TrimSpace(c.Bucket) == "" {
			return fmt.Errorf("%w: endpoint and bucket are required for %s", ErrProviderNotConfigured, c.Provider)
		}
		if strings.TrimSpace(c.AccessKey) == "" || strings.TrimSpace(c.SecretKey) == "" {
			return fmt.Errorf("%w: access and secret keys are required for %s", ErrProviderNotConfigured, c.Provider)
		}
	default:
		return fmt.Errorf("%w: unsupported provider %q", ErrProviderNotConfigured, c.Provider)
	}
	return nil
}

func NewProvider(config Config) (StorageProvider, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	switch ProviderKind(strings.ToLower(strings.TrimSpace(string(config.Provider)))) {
	case "", ProviderLocal:
		return NewLocalProvider(config.LocalRoot)
	case ProviderS3:
		return NewS3Provider(config)
	case ProviderMinIO:
		return NewMinIOProvider(config)
	default:
		return nil, fmt.Errorf("%w: unsupported provider %q", ErrProviderNotConfigured, config.Provider)
	}
}

// RemoteProvider is an intentionally dependency-free S3-compatible contract
// stub. The application can add an AWS SDK adapter without changing domain
// code; until that adapter is activated, operations fail explicitly instead
// of silently writing to a different backend.
type RemoteProvider struct {
	config Config
}

type S3Provider = RemoteProvider
type MinIOProvider = RemoteProvider

func NewS3Provider(config Config) (*RemoteProvider, error) {
	config.Provider = ProviderS3
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &RemoteProvider{config: config}, nil
}

func NewMinIOProvider(config Config) (*RemoteProvider, error) {
	config.Provider = ProviderMinIO
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &RemoteProvider{config: config}, nil
}

func (p *RemoteProvider) Put(_ context.Context, key string, _ io.Reader, _ PutOptions) (ObjectInfo, error) {
	if err := ValidateKey(key); err != nil {
		return ObjectInfo{}, err
	}
	return ObjectInfo{}, fmt.Errorf("%w: %s adapter is not activated", ErrProviderNotConfigured, p.config.Provider)
}

func (p *RemoteProvider) Open(_ context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	if err := ValidateKey(key); err != nil {
		return nil, ObjectInfo{}, err
	}
	return nil, ObjectInfo{}, fmt.Errorf("%w: %s adapter is not activated", ErrProviderNotConfigured, p.config.Provider)
}

func (p *RemoteProvider) Stat(_ context.Context, key string) (ObjectInfo, error) {
	if err := ValidateKey(key); err != nil {
		return ObjectInfo{}, err
	}
	return ObjectInfo{}, fmt.Errorf("%w: %s adapter is not activated", ErrProviderNotConfigured, p.config.Provider)
}

func (p *RemoteProvider) Delete(_ context.Context, key string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	return fmt.Errorf("%w: %s adapter is not activated", ErrProviderNotConfigured, p.config.Provider)
}

func (p *RemoteProvider) Config() Config {
	if p == nil {
		return Config{}
	}
	return p.config
}
