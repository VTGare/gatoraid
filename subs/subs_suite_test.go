package subs_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSubs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Subs Suite")
}
