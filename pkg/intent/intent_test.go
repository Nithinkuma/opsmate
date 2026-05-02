package intent_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nithinkuma/opsmate/pkg/intent"
	"github.com/stretchr/testify/require"
)

func validIntent() *intent.Intent {
	return &intent.Intent{
		IntentID:      "01HZK9ABCDEFGHJKMNPQRSTVWX",
		SchemaVersion: "1",
		Source: intent.Source{
			System:    "jira",
			TicketID:  "INFRA-1234",
			URL:       "https://yourorg.atlassian.net/browse/INFRA-1234",
			Reporter:  "alice@org.com",
			FetchedAt: time.Now().UTC(),
		},
		Action: intent.Action{
			Verb: "update_dependency",
			Target: intent.Target{
				Repo:   "org/payments-service",
				Branch: "main",
				Scope:  "package.json",
			},
			Parameters: map[string]interface{}{
				"package":    "lodash",
				"to_version": "4.17.21",
			},
		},
		Context: intent.Context{
			DescriptionRaw: "Bump lodash to fix CVE-2021-23337",
			LinkedTickets:  []string{"SEC-892"},
			Priority:       "P3",
		},
		Policy: intent.Policy{
			AutoMerge:            false,
			RequireHumanApproval: true,
			Approvers:            []string{"@infra-leads"},
		},
	}
}

func TestValidate_Valid(t *testing.T) {
	err := intent.Validate(validIntent())
	require.NoError(t, err)
}

func TestValidate_MissingSchemaVersion(t *testing.T) {
	i := validIntent()
	i.SchemaVersion = ""
	err := intent.Validate(i)
	require.Error(t, err)
}

func TestValidate_InvalidVerb(t *testing.T) {
	i := validIntent()
	i.Action.Verb = "destroy_everything"
	err := intent.Validate(i)
	require.Error(t, err)
}

func TestValidate_MissingRepo(t *testing.T) {
	i := validIntent()
	i.Action.Target.Repo = ""
	// repo is required in target
	err := intent.Validate(i)
	require.Error(t, err)
}

func TestRoundTrip(t *testing.T) {
	original := validIntent()

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded intent.Intent
	require.NoError(t, json.Unmarshal(data, &decoded))

	require.Equal(t, original.IntentID, decoded.IntentID)
	require.Equal(t, original.Action.Verb, decoded.Action.Verb)
	require.Equal(t, original.Source.TicketID, decoded.Source.TicketID)
}

func TestValidate_AllSeedVerbs(t *testing.T) {
	verbs := []string{
		"update_dependency", "bump_version", "add_yaml_field",
		"remove_yaml_field", "rename_resource", "enable_feature_flag",
		"disable_feature_flag", "add_terraform_module", "update_terraform_var",
		"add_helm_value", "rotate_secret_ref", "update_image_tag",
		"add_codeowner", "update_ci_config", "add_env_var",
	}
	for _, verb := range verbs {
		t.Run(verb, func(t *testing.T) {
			i := validIntent()
			i.Action.Verb = verb
			err := intent.Validate(i)
			require.NoError(t, err)
		})
	}
}
