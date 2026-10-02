package moderation_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestModeration(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Moderation Suite")
}
