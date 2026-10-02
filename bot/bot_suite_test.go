package bot

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestBot(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Bot Suite")
}
