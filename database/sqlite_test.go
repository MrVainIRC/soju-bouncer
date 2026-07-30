//go:build !nosqlite

package database

import (
	"context"
	"database/sql"
	"testing"
)

// SQLite version 0 schema. DO NOT EDIT.
const sqliteV0Schema = `
CREATE TABLE User (
	username VARCHAR(255) NOT NULL UNIQUE,
	password VARCHAR(255)
);

CREATE TABLE Network (
	id INTEGER PRIMARY KEY,
	name VARCHAR(255),
	user VARCHAR(255) NOT NULL,
	addr VARCHAR(255) NOT NULL,
	nick VARCHAR(255) NOT NULL,
	username VARCHAR(255),
	realname VARCHAR(255),
	pass VARCHAR(255),
	sasl_mechanism VARCHAR(255),
	sasl_plain_username VARCHAR(255),
	sasl_plain_password VARCHAR(255),
	UNIQUE(user, addr, nick),
	UNIQUE(user, name)
);

CREATE TABLE Channel (
	id INTEGER PRIMARY KEY,
	network INTEGER NOT NULL,
	name VARCHAR(255) NOT NULL,
	key VARCHAR(255),
	FOREIGN KEY(network) REFERENCES Network(id),
	UNIQUE(network, name)
);

PRAGMA user_version = 1;
`

func TestSqliteMigrations(t *testing.T) {
	sqlDB, err := sql.Open(sqliteDriver, ":memory:")
	if err != nil {
		t.Fatalf("failed to create temporary SQLite database: %v", err)
	}

	if _, err := sqlDB.Exec(sqliteV0Schema); err != nil {
		t.Fatalf("DB.Exec() failed for v0 schema: %v", err)
	}

	db := &SqliteDB{db: sqlDB}
	defer db.Close()

	if err := db.upgrade(); err != nil {
		t.Fatalf("SqliteDB.Upgrade() failed: %v", err)
	}
}

func TestSqliteNetworkMetadata(t *testing.T) {
	db, err := OpenTempSqliteDB()
	if err != nil {
		t.Fatalf("failed to create temporary SQLite database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	user := NewUser("user")
	if err := db.StoreUser(ctx, user); err != nil {
		t.Fatalf("StoreUser() failed: %v", err)
	}
	network := NewNetwork("irc.example.org")
	if err := db.StoreNetwork(ctx, user.ID, network); err != nil {
		t.Fatalf("StoreNetwork() failed: %v", err)
	}

	value := "online"
	if err := db.StoreNetworkMetadata(ctx, network.ID, "status", &value); err != nil {
		t.Fatalf("StoreNetworkMetadata() failed: %v", err)
	}
	value = "https://example.org/avatar.png"
	if err := db.StoreNetworkMetadata(ctx, network.ID, "avatar", &value); err != nil {
		t.Fatalf("StoreNetworkMetadata() update failed: %v", err)
	}

	metadata, err := db.ListNetworkMetadata(ctx, network.ID)
	if err != nil {
		t.Fatalf("ListNetworkMetadata() failed: %v", err)
	}
	if len(metadata) != 2 || metadata[0].Key != "avatar" || metadata[0].Value != "https://example.org/avatar.png" || metadata[1].Key != "status" || metadata[1].Value != "online" {
		t.Fatalf("unexpected metadata: %#v", metadata)
	}

	if err := db.StoreNetworkMetadata(ctx, network.ID, "status", nil); err != nil {
		t.Fatalf("StoreNetworkMetadata() delete failed: %v", err)
	}
	metadata, err = db.ListNetworkMetadata(ctx, network.ID)
	if err != nil {
		t.Fatalf("ListNetworkMetadata() after delete failed: %v", err)
	}
	if len(metadata) != 1 || metadata[0].Key != "avatar" {
		t.Fatalf("unexpected metadata after delete: %#v", metadata)
	}

	if err := db.ClearNetworkMetadata(ctx, network.ID); err != nil {
		t.Fatalf("ClearNetworkMetadata() failed: %v", err)
	}
	metadata, err = db.ListNetworkMetadata(ctx, network.ID)
	if err != nil {
		t.Fatalf("ListNetworkMetadata() after clear failed: %v", err)
	}
	if len(metadata) != 0 {
		t.Fatalf("unexpected metadata after clear: %#v", metadata)
	}
}

func TestSqliteClientNetworkMetadata(t *testing.T) {
	db, err := OpenTempSqliteDB()
	if err != nil {
		t.Fatalf("failed to create temporary SQLite database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	user := NewUser("user")
	if err := db.StoreUser(ctx, user); err != nil {
		t.Fatalf("StoreUser() failed: %v", err)
	}
	network := NewNetwork("irc.example.org")
	if err := db.StoreNetwork(ctx, user.ID, network); err != nil {
		t.Fatalf("StoreNetwork() failed: %v", err)
	}

	desktop, mobile := "desktop", "mobile"
	if err := db.StoreClientNetworkMetadata(ctx, network.ID, "desktop", "status", &desktop); err != nil {
		t.Fatalf("StoreClientNetworkMetadata(desktop) failed: %v", err)
	}
	if err := db.StoreClientNetworkMetadata(ctx, network.ID, "mobile", "status", &mobile); err != nil {
		t.Fatalf("StoreClientNetworkMetadata(mobile) failed: %v", err)
	}
	metadata, err := db.ListClientNetworkMetadata(ctx, network.ID, "desktop")
	if err != nil {
		t.Fatalf("ListClientNetworkMetadata() failed: %v", err)
	}
	if len(metadata) != 1 || metadata[0].Value != desktop {
		t.Fatalf("desktop metadata = %#v, want status=%q", metadata, desktop)
	}
	if err := db.ClearClientNetworkMetadata(ctx, network.ID, "desktop"); err != nil {
		t.Fatalf("ClearClientNetworkMetadata() failed: %v", err)
	}
	metadata, err = db.ListClientNetworkMetadata(ctx, network.ID, "desktop")
	if err != nil || len(metadata) != 0 {
		t.Fatalf("desktop metadata after clear = %#v, %v", metadata, err)
	}
	metadata, err = db.ListClientNetworkMetadata(ctx, network.ID, "mobile")
	if err != nil || len(metadata) != 1 || metadata[0].Value != mobile {
		t.Fatalf("mobile metadata after desktop clear = %#v, %v", metadata, err)
	}
}
