package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/backend/control-plane/internal/secrets"
)

// KeyCipher abstracts the sealing primitive (implemented by secrets.Key;
// nil key = honest ErrKeyNotConfigured).
type KeyCipher interface {
	Encrypt(plaintext []byte) ([]byte, error)
	Decrypt(ciphertext []byte) ([]byte, error)
}

// compile-time guard: secrets.Key implements the cipher.
var _ KeyCipher = secrets.Key{}

// Service implements the cloud provider use cases.
type Service struct {
	Store Store
	// Key seals credential material. Zero value (nil interface) = operations
	// that need credentials are refused honestly.
	Key KeyCipher
	// Providers maps kind → adapter (registered in main.go).
	Providers map[Kind]Provider
}

// NewService builds the cloud service with the default adapter set.
func NewService(store Store, key KeyCipher) *Service {
	return &Service{
		Store:     store,
		Key:       key,
		Providers: map[Kind]Provider{KindS3: &S3Provider{}},
	}
}

// CreateInput is the validated registration request.
type CreateInput struct {
	Name        string            `json:"name"`
	Kind        Kind              `json:"kind"`
	Endpoint    string            `json:"endpoint"`
	Region      string            `json:"region"`
	Credentials map[string]string `json:"credentials"`
}

// Create registers a provider. Credentials are validated against the
// adapter's required keys, sealed and never returned.
func (s *Service) Create(ctx context.Context, in CreateInput) (ProviderRow, error) {
	p, ok := s.Providers[in.Kind]
	if !ok {
		return ProviderRow{}, fmt.Errorf("%w: %q", ErrUnknownKind, in.Kind)
	}
	if len(in.Name) < 3 || len(in.Name) > 64 {
		return ProviderRow{}, ErrInvalidName
	}
	if _, err := validateEndpoint(in.Endpoint); err != nil {
		return ProviderRow{}, err
	}
	if s.Key == nil {
		return ProviderRow{}, ErrKeyNotConfigured
	}
	if err := providerCredKeys(p, in.Credentials); err != nil {
		return ProviderRow{}, err
	}
	plain, err := json.Marshal(Credentials{
		Endpoint:        in.Endpoint,
		Region:          in.Region,
		AccessKeyID:     in.Credentials["access_key_id"],
		SecretAccessKey: in.Credentials["secret_access_key"],
	})
	if err != nil {
		return ProviderRow{}, err
	}
	sealed, err := s.Key.Encrypt(plain)
	if err != nil {
		return ProviderRow{}, fmt.Errorf("cloud: kimlik bilgisi seal: %w", err)
	}
	keys := make([]string, 0, len(in.Credentials))
	for k := range in.Credentials {
		keys = append(keys, k)
	}
	return s.Store.Create(ctx, in.Name, in.Kind, in.Endpoint, in.Region, sealed, keys)
}

// List returns registrations (write-only credential contract).
func (s *Service) List(ctx context.Context) ([]ProviderRow, error) {
	return s.Store.List(ctx)
}

// Get returns one registration.
func (s *Service) Get(ctx context.Context, id string) (ProviderRow, error) {
	return s.Store.Get(ctx, id)
}

// DeleteProvider removes the registration (audit handled by the HTTP layer).
func (s *Service) DeleteProvider(ctx context.Context, id string) error {
	return s.Store.Delete(ctx, id)
}

// decryptCreds fetches and opens the sealed credential blob.
func (s *Service) decryptCreds(ctx context.Context, id string) (Credentials, error) {
	storer, ok := s.Store.(interface {
		EncryptedCredentials(ctx context.Context, id string) ([]byte, error)
	})
	if !ok {
		return Credentials{}, fmt.Errorf("cloud: store şifreli kimlik bilgisi sağlamıyor")
	}
	sealed, err := storer.EncryptedCredentials(ctx, id)
	if err != nil {
		return Credentials{}, err
	}
	plain, err := s.Key.Decrypt(sealed)
	if err != nil {
		return Credentials{}, fmt.Errorf("cloud: kimlik bilgisi açma: %w", err)
	}
	var c Credentials
	if err := json.Unmarshal(plain, &c); err != nil {
		return Credentials{}, fmt.Errorf("cloud: kimlik bilgisi şeması bozuk: %w", err)
	}
	return c, nil
}

// VerifyCredentials probes the stored credential against the provider and
// returns the honest matrix + probe report.
func (s *Service) VerifyCredentials(ctx context.Context, id string) (VerifyReport, error) {
	prow, err := s.Store.Get(ctx, id)
	if err != nil {
		return VerifyReport{}, err
	}
	p, ok := s.Providers[prow.Kind]
	if !ok {
		return VerifyReport{}, fmt.Errorf("%w: %q", ErrUnknownKind, prow.Kind)
	}
	if s.Key == nil {
		return VerifyReport{}, ErrKeyNotConfigured
	}
	creds, err := s.decryptCreds(ctx, id)
	if err != nil {
		return VerifyReport{}, err
	}
	return p.VerifyCredentials(ctx, creds)
}

// ListResources lists provider resources visible to the stored credential.
func (s *Service) ListResources(ctx context.Context, id string, limit int) ([]Resource, error) {
	prow, err := s.Store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	p, ok := s.Providers[prow.Kind]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownKind, prow.Kind)
	}
	if s.Key == nil {
		return nil, ErrKeyNotConfigured
	}
	creds, err := s.decryptCreds(ctx, id)
	if err != nil {
		return nil, err
	}
	return p.ListResources(ctx, creds, limit)
}

// DeleteResource deletes a provider resource. confirm MUST be true — the
// confirmation gate is enforced HERE (service boundary), not only in the UI.
func (s *Service) DeleteResource(ctx context.Context, id, name string, confirm bool) error {
	if !confirm {
		return ErrConfirmationRequired
	}
	prow, err := s.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	p, ok := s.Providers[prow.Kind]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownKind, prow.Kind)
	}
	if s.Key == nil {
		return ErrKeyNotConfigured
	}
	creds, err := s.decryptCreds(ctx, id)
	if err != nil {
		return err
	}
	return p.DeleteResource(ctx, creds, name)
}

// providerCredKeys checks required credential fields are present.
func providerCredKeys(p Provider, creds map[string]string) error {
	for _, k := range p.RequiredCredentialKeys() {
		if len(creds[k]) == 0 {
			return fmt.Errorf("%w: %s zorunlu", ErrCredIncomplete, k)
		}
		if len(creds[k]) > 512 {
			return fmt.Errorf("%w: %s çok uzun", ErrCredIncomplete, k)
		}
	}
	return nil
}

// marshalKeys / unmarshalKeys encode the credential KEY NAMES (values never).
func marshalKeys(keys []string) []byte {
	if keys == nil {
		keys = []string{}
	}
	b, _ := json.Marshal(keys)
	return b
}

func unmarshalKeys(b []byte) []string {
	var keys []string
	if err := json.Unmarshal(b, &keys); err != nil {
		return []string{}
	}
	return keys
}

// compile-time: NowFunc reference kept for adapter tests.
var _ NowFunc = time.Now
