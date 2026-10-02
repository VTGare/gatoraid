package holodex_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestHolodex(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Holodex Suite")
}
