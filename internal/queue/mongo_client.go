package queue

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/jaysongiroux/hook_way/internal/config"
)

// errMongoDuplicateKey is the sentinel goMongoOps.InsertOne returns when Mongo
// reports a duplicate-key (E11000) error. The publisher treats it as a no-op so
// at-least-once retries do not error (first-write-wins).
var errMongoDuplicateKey = errors.New("mongo: duplicate key")

// mongoOps is the thin, mockable interface the Mongo publisher depends on.
type mongoOps interface {
	// InsertOne inserts doc into the named collection. A duplicate-key error is
	// returned as errMongoDuplicateKey so the caller can treat it as a no-op.
	InsertOne(ctx context.Context, collection string, doc any) error
	// Ping checks liveness of the connection.
	Ping(ctx context.Context) error
	// Close releases the client/connection pool.
	Close() error
}

// goMongoOps wraps a *mongo.Client bound to a database and implements mongoOps.
type goMongoOps struct {
	client *mongo.Client
	db     string
}

func (m *goMongoOps) InsertOne(ctx context.Context, collection string, doc any) error {
	_, err := m.client.Database(m.db).Collection(collection).InsertOne(ctx, doc)
	if err != nil && mongo.IsDuplicateKeyError(err) {
		return errMongoDuplicateKey
	}
	return err
}

func (m *goMongoOps) Ping(ctx context.Context) error { return m.client.Ping(ctx, nil) }

func (m *goMongoOps) Close() error { return m.client.Disconnect(context.Background()) }

// newMongoClient builds a mongoOps from the destination's MongoConfig. The URI
// carries hosts/auth/replicaset/tls; an explicit TLS block (any field set)
// overrides via SetTLSConfig; MaxPoolSize caps the pool when > 0. A Ping
// confirms the connection before returning (fail-fast).
func newMongoClient(ctx context.Context, cfg config.MongoConfig) (mongoOps, error) {
	opts := options.Client().ApplyURI(cfg.URI)
	if cfg.MaxPoolSize > 0 {
		opts.SetMaxPoolSize(cfg.MaxPoolSize)
	}
	if mongoTLSSet(cfg.TLS) {
		tlsCfg, err := buildMongoTLSConfig(cfg.TLS)
		if err != nil {
			return nil, err
		}
		opts.SetTLSConfig(tlsCfg)
	}
	client, err := mongo.Connect(opts)
	if err != nil {
		return nil, fmt.Errorf("mongodb: connect: %w", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("mongodb: ping: %w", err)
	}
	return &goMongoOps{client: client, db: cfg.Database}, nil
}

func mongoTLSSet(c config.MongoTLSConfig) bool {
	return c.CACert != "" || c.ClientCert != "" || c.ClientKey != "" ||
		c.ServerName != "" || c.InsecureSkipVerify
}

// buildMongoTLSConfig assembles a *tls.Config from the Mongo TLS options.
// Certificate loading is shared through tlsfiles.Files.Build.
func buildMongoTLSConfig(c config.MongoTLSConfig) (*tls.Config, error) {
	m := tlsMaterial{CACert: c.CACert, ClientCert: c.ClientCert, ClientKey: c.ClientKey, ServerName: c.ServerName, InsecureSkipVerify: c.InsecureSkipVerify}
	return newTLSConfig("mongodb", m)
}
