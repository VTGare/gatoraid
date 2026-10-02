package translate_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTranslate(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Translate Suite")
}
