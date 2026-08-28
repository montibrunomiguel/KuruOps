package dbmigrate_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kuruops/kuruops/internal/dbmigrate"
)

func TestTargetConfig_DSN(t *testing.T) {
	t.Run("builds a standard postgres DSN", func(t *testing.T) {
		target := dbmigrate.TargetConfig{
			Host: "db.example.com", Port: 5432, Database: "kuruops",
			User: "postgres", Password: "s3cret", SSLMode: "require",
		}
		assert.Equal(t, "postgres://postgres:s3cret@db.example.com:5432/kuruops?sslmode=require", target.DSN())
	})

	t.Run("defaults sslmode to disable", func(t *testing.T) {
		target := dbmigrate.TargetConfig{Host: "localhost", Port: 5432, Database: "kuruops", User: "postgres", Password: "x"}
		assert.Equal(t, "postgres://postgres:x@localhost:5432/kuruops?sslmode=disable", target.DSN())
	})

	t.Run("URL-escapes special characters in credentials", func(t *testing.T) {
		target := dbmigrate.TargetConfig{
			Host: "localhost", Port: 5432, Database: "kuruops",
			User: "postgres", Password: "p@ss/word?",
		}
		assert.Equal(t, "postgres://postgres:p%40ss%2Fword%3F@localhost:5432/kuruops?sslmode=disable", target.DSN())
	})
}
