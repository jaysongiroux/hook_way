import { Alert, Modal, MultiSelect, Stack, Tabs, Text } from "@mantine/core";
import { DARK } from "../tokens";
import { SchemaForm } from "./SchemaForm";
import { useGraphStore } from "./store";
import workspaceCatalog from "./workspaceSchema.json";
import {
  isSchemaObject,
  schemaProperties,
  type JSONSchema,
} from "./nodeSchema";
import { record, routingNameError, WorkspaceEntries } from "./WorkspaceEntries";

const routingSchema = workspaceCatalog.routing as JSONSchema;
const categories = ["profiles", "environments", "stores"] as const;
const labels = {
  profiles: "Routing profiles",
  environments: "Environments",
  stores: "Routing stores",
};
const singular = {
  profiles: "routing profile",
  environments: "environment",
  stores: "routing store",
};
const required = {
  profiles: ["account_id", "credential_mode"],
  environments: ["profiles", "accounts", "destination_prefix"],
  stores: ["table"],
};
const help: Record<string, string> = {
  account_id: "The 12-digit AWS account this routing profile admits.",
  credential_mode:
    "An admission label, using lowercase letters, digits and hyphens. This is not an outbound credential profile or secret provider.",
  profiles:
    "Routing profiles allowed to select this environment. Configure profiles first.",
  accounts:
    "Allowed 12-digit AWS account IDs. Each selected routing profile's account must appear here.",
  aliases:
    "Optional unique alternate environment names: lowercase letters, digits and hyphens.",
  destination_prefix:
    "Prefix for broker target names: letters, digits, underscore or hyphen, up to 64 characters.",
  backend_host:
    "Optional DNS hostname for admitted HTTP upstreams. Do not include a scheme, port or path.",
  table:
    "DynamoDB table used to look up and register typed route records. Uses the process AWS credential chain.",
  region: "Optional AWS region; omitted uses the process AWS configuration.",
  endpoint:
    "Optional HTTP(S) endpoint override for local testing, without credentials, query or fragment.",
  timeout: "Go duration greater than zero and at most 30s; omitted uses 2s.",
  id_field: "Table field for route ID; omitted uses id.",
  type_field: "Table field for route ID type; omitted uses id_type.",
  environment_field:
    "Table field for canonical environment name; omitted uses env_name.",
  created_at_field:
    "Table field for creation time; omitted uses created_at. All four table field names must be distinct.",
};

export function routingEntrySchema(
  category: (typeof categories)[number],
): JSONSchema {
  const map = schemaProperties(routingSchema)[category];
  const entry = isSchemaObject(map) ? map.additionalProperties : undefined;
  return isSchemaObject(entry)
    ? { ...entry, required: required[category] }
    : false;
}

export function RoutingPanel({
  opened,
  onClose,
}: {
  opened: boolean;
  onClose: () => void;
}) {
  const configExtra = useGraphStore((s) => s.configExtra);
  const setConfigExtra = useGraphStore((s) => s.setConfigExtra);
  const routing = record(configExtra.routing);
  const profileNames = Object.keys(record(routing.profiles));
  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title="Routing"
      size="xl"
      closeButtonProps={{ "aria-label": "Close Routing" }}
    >
      <Stack gap="md">
        <Text size="sm" c={DARK.muted}>
          Configure admission before using route, upstream, registration or
          batch nodes: create a routing profile, then an environment that
          permits that profile and its account. Add a routing store when you
          need stored lookups or registration. This edits configuration; it does
          not create AWS resources or test access.
        </Text>
        <Tabs defaultValue="profiles" keepMounted={false}>
          <Tabs.List>
            {categories.map((category) => (
              <Tabs.Tab key={category} value={category}>
                {labels[category]}
              </Tabs.Tab>
            ))}
          </Tabs.List>
          {categories.map((category) => (
            <Tabs.Panel key={category} value={category} pt="md">
              <WorkspaceEntries
                label={singular[category]}
                value={routing[category]}
                nameError={routingNameError}
                initial={() =>
                  category === "environments"
                    ? { profiles: [], accounts: [] }
                    : {}
                }
                warning="Removing or renaming an entry does not rewrite nodes or other workspace references. Validate the exported document after changing these entries."
                onChange={(entries) =>
                  setConfigExtra({
                    routing: { ...routing, [category]: entries },
                  })
                }
              >
                {(name, entry, change) => (
                  <Stack gap="sm">
                    {category === "environments" && (
                      <>
                        {profileNames.length === 0 && (
                          <Alert color="orange">
                            Create a routing profile in the Routing profiles tab
                            before configuring environment admission.
                          </Alert>
                        )}
                        <MultiSelect
                          label="Allowed routing profiles"
                          required
                          searchable
                          data={[
                            ...new Set([
                              ...profileNames,
                              ...(Array.isArray(entry.profiles)
                                ? entry.profiles.map(String)
                                : []),
                            ]),
                          ]}
                          value={
                            Array.isArray(entry.profiles)
                              ? entry.profiles.map(String)
                              : []
                          }
                          error={
                            !Array.isArray(entry.profiles) ||
                            entry.profiles.length === 0
                              ? "Choose at least one routing profile."
                              : entry.profiles.some(
                                    (name) =>
                                      !profileNames.includes(String(name)),
                                  )
                                ? "An imported routing profile is not configured. Create it or remove its reference."
                                : undefined
                          }
                          description={help.profiles}
                          onChange={(next) =>
                            change({ ...entry, profiles: next })
                          }
                        />
                      </>
                    )}
                    <SchemaForm
                      schema={routingEntrySchema(category)}
                      value={entry}
                      onChange={(next) => change(record(next))}
                      scope={`routing/${category}/${encodeURIComponent(name)}`}
                      path={`routing.${category}.${name}`}
                      fieldHelp={(key) => help[key]}
                      hideFields={
                        category === "environments" ? ["profiles"] : undefined
                      }
                      defaults={
                        category === "stores"
                          ? {
                              timeout: "2s",
                              id_field: "id",
                              type_field: "id_type",
                              environment_field: "env_name",
                              created_at_field: "created_at",
                            }
                          : undefined
                      }
                      order={
                        category === "environments"
                          ? [
                              "profiles",
                              "accounts",
                              "destination_prefix",
                              "backend_host",
                              "aliases",
                            ]
                          : required[category]
                      }
                      labels={{
                        account_id: "AWS account ID",
                        credential_mode: "Credential admission label",
                        profiles: "Allowed routing profiles",
                        accounts: "Allowed AWS accounts",
                        table: "DynamoDB table",
                      }}
                    />
                  </Stack>
                )}
              </WorkspaceEntries>
            </Tabs.Panel>
          ))}
        </Tabs>
      </Stack>
    </Modal>
  );
}
