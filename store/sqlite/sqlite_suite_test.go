package sqlite_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSQLite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "SQLite Store Suite")
}
