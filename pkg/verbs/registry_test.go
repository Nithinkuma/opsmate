package verbs_test

import (
	"testing"

	"github.com/nithinkuma/opsmate/pkg/verbs"
	"github.com/stretchr/testify/require"
)

func TestLoadSeed(t *testing.T) {
	r, err := verbs.LoadSeed()
	require.NoError(t, err)
	require.NotNil(t, r)

	known := r.KnownVerbs()
	require.Len(t, known, 15, "expect all 15 seed verbs")
}

func TestHas(t *testing.T) {
	r, err := verbs.LoadSeed()
	require.NoError(t, err)

	require.True(t, r.Has("update_dependency"))
	require.False(t, r.Has("destroy_everything"))
}

func TestValidate_UpdateDependency_Valid(t *testing.T) {
	r, err := verbs.LoadSeed()
	require.NoError(t, err)

	err = r.Validate("update_dependency", map[string]interface{}{
		"package":    "lodash",
		"to_version": "4.17.21",
	})
	require.NoError(t, err)
}

func TestValidate_UpdateDependency_MissingRequired(t *testing.T) {
	r, err := verbs.LoadSeed()
	require.NoError(t, err)

	err = r.Validate("update_dependency", map[string]interface{}{
		"package": "lodash",
		// missing to_version
	})
	require.Error(t, err)
}

func TestValidate_UnknownVerb(t *testing.T) {
	r, err := verbs.LoadSeed()
	require.NoError(t, err)

	err = r.Validate("not_a_verb", map[string]interface{}{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown verb")
}

func TestValidate_AllSeedVerbs(t *testing.T) {
	r, err := verbs.LoadSeed()
	require.NoError(t, err)

	cases := map[string]map[string]interface{}{
		"update_dependency":    {"package": "lodash", "to_version": "4.17.21"},
		"bump_version":         {"version": "1.2.3"},
		"add_yaml_field":       {"file_path": "values.yaml", "field_path": "image.tag", "value": "v1.0"},
		"remove_yaml_field":    {"file_path": "values.yaml", "field_path": "old.key"},
		"rename_resource":      {"resource_type": "service", "old_name": "foo", "new_name": "bar"},
		"enable_feature_flag":  {"flag_name": "new-checkout"},
		"disable_feature_flag": {"flag_name": "old-flow"},
		"add_terraform_module": {"module_name": "vpc", "source": "terraform-aws-modules/vpc/aws", "version": "5.0.0"},
		"update_terraform_var": {"var_name": "instance_type", "value": "t3.medium"},
		"add_helm_value":       {"chart_path": "charts/app", "key": "replicaCount", "value": "3"},
		"rotate_secret_ref":    {"secret_name": "db-password", "new_ref": "vault:secret/db#password"},
		"update_image_tag":     {"image": "nginx", "tag": "1.27"},
		"add_codeowner":        {"pattern": "docs/", "owners": []interface{}{"@docs-team"}},
		"update_ci_config":     {"pipeline_file": ".github/workflows/ci.yml", "changes": map[string]interface{}{"timeout-minutes": 30}},
		"add_env_var":          {"var_name": "LOG_LEVEL", "value": "debug"},
	}

	for verb, params := range cases {
		t.Run(verb, func(t *testing.T) {
			err := r.Validate(verb, params)
			require.NoError(t, err, "verb %q with params %v", verb, params)
		})
	}
}
