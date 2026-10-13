package testenv

import "testing"

func TestRequireContainerNilError(t *testing.T) {
	RequireContainer(t, nil, "rabbitmq")
}
