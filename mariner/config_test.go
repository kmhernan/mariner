package mariner

import (
	"encoding/json"
        "testing"

        "github.com/stretchr/testify/assert"

	k8sv1 "k8s.io/api/core/v1"
)

func Test_loadConfig(t *testing.T) {

	t.Run("Should return the correct config object", func(t *testing.T) {
		config := loadConfig("../testdata/test-config.json")
		assert.Equal(t, "mariner-engine", config.Containers.Engine.Name)
	})
}

func Test_restartPolicy(t *testing.T) {
	t.Run("Should return k8sv1.RestartPolicyNever", func(t *testing.T) {
		jsonStr := `
{
  "labels": {"key": "value"},
  "serviceaccount": "test",
  "restart_policy": "never"
}
`
		conf := JobConfig{}
		json.Unmarshal([]byte(jsonStr), &conf)
		res := conf.restartPolicy()
		assert.Equal(t, k8sv1.RestartPolicyNever, res)
	})

	t.Run("Should return k8sv1.RestartPolicyAlways", func(t *testing.T) {
		jsonStr := `
{
  "labels": {"key": "value"},
  "serviceaccount": "test",
  "restart_policy": "always"
}
`
		conf := JobConfig{}
		json.Unmarshal([]byte(jsonStr), &conf)
		res := conf.restartPolicy()
		assert.Equal(t, k8sv1.RestartPolicyAlways, res)
	})
}

func Test_volumeMount(t *testing.T) {
	t.Run("Should set MountPropagation to k8sv1.MountPropagationHostToContainer and ReadOnly to false", func(t *testing.T) {
		res := volumeMount(engineWorkspaceVolumeName, marinerTask)
		assert.False(t, res.ReadOnly)
		assert.Equal(t, &mountPropagationHostToContainer, res.MountPropagation)

		res = volumeMount(engineWorkspaceVolumeName, marinerEngine)
		assert.False(t, res.ReadOnly)
		assert.Equal(t, &mountPropagationHostToContainer, res.MountPropagation)
	})
}
