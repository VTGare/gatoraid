package helix_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestHelix(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Helix Suite")
}
