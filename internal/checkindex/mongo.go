package checkindex

import (
	"context"
	"crypto/tls"
	"errors"
	"net/url"
	"os"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

// Direct connections retain URI transport/auth options, but never retain SRV
// discovery or replica-set routing. This prevents per-node statistics from
// accidentally being collected from whichever server the seed pool selects.
func clientOptions(p Profile, host string) (*options.ClientOptions, error) {
	u, err := url.Parse(p.InitialURI)
	if err != nil || u == nil || u.Host == "" || (u.Scheme != "mongodb" && u.Scheme != "mongodb+srv") {
		return nil, errors.New("invalid MongoDB URI in selected cluster")
	}
	srv := u.Scheme == "mongodb+srv"
	if host != "" {
		if mapped := p.HostMapping[host]; mapped != "" {
			host = mapped
		}
		u.Scheme = "mongodb"
		u.Host = host
		u.User = nil
		q := u.Query()
		for k := range q {
			switch strings.ToLower(k) {
			case "replicaset", "directconnection", "loadbalanced", "srvmaxhosts", "srvservicename":
				q.Del(k)
			}
		}
		if srv && !q.Has("tls") && !q.Has("ssl") {
			q.Set("tls", "true")
		}
		u.RawQuery = q.Encode()
	}
	opts := options.Client().ApplyURI(u.String()).SetTimeout(20 * time.Second).SetServerSelectionTimeout(10 * time.Second).SetConnectTimeout(10 * time.Second).SetMaxPoolSize(2)
	auth := options.Credential{}
	if opts.Auth != nil {
		auth = *opts.Auth
	}
	auth.Username = p.Username
	auth.Password = p.Password
	auth.PasswordSet = true
	auth.AuthSource = p.AuthSource
	opts.SetAuth(auth)
	if host != "" {
		opts.SetDirect(true).SetReadPreference(readpref.SecondaryPreferred()).SetRetryWrites(false)
	}
	if os.Getenv("MONGO_TLS") == "true" && opts.TLSConfig == nil {
		opts.SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12})
	}
	if opts.Validate() != nil {
		return nil, errors.New("invalid MongoDB client options; check selected cluster URI")
	}
	return opts, nil
}

type mongoDB struct{ client *mongo.Client }

func dialMongo(_ context.Context, p Profile, host string) (Database, error) {
	opts, err := clientOptions(p, host)
	if err != nil {
		return nil, err
	}
	client, err := mongo.Connect(opts)
	if err != nil {
		return nil, errors.New("MongoDB client setup failed")
	}
	return &mongoDB{client}, nil
}
func (m *mongoDB) Hello(ctx context.Context) (Hello, error) {
	var h Hello
	err := m.client.Database("admin").RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}, options.RunCmd().SetReadPreference(readpref.SecondaryPreferred())).Decode(&h)
	return h, err
}
func (m *mongoDB) Status(ctx context.Context) (Status, error) {
	var s Status
	err := m.client.Database("admin").RunCommand(ctx, bson.D{{Key: "replSetGetStatus", Value: 1}}, options.RunCmd().SetReadPreference(readpref.SecondaryPreferred())).Decode(&s)
	return s, err
}
func (m *mongoDB) Collections(ctx context.Context, db string) ([]string, error) {
	return m.client.Database(db).ListCollectionNames(ctx, bson.D{}, options.ListCollections().SetNameOnly(true))
}
func (m *mongoDB) Stats(ctx context.Context, db, name string) ([]IndexStat, error) {
	cursor, err := m.client.Database(db).Collection(name).Aggregate(ctx, mongo.Pipeline{bson.D{{Key: "$indexStats", Value: bson.D{}}}})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var rows []IndexStat
	err = cursor.All(ctx, &rows)
	return rows, err
}
func (m *mongoDB) Close(ctx context.Context) error { return m.client.Disconnect(ctx) }
