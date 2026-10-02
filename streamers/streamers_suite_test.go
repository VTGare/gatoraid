package streamers_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestStreamers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Streamers Suite")
}
