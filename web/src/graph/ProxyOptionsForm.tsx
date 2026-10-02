import {
  Alert,
  Button,
  Fieldset,
  Select,
  Stack,
  Switch,
  Text,
} from "@mantine/core";
import { useGraphStore } from "./store";
import { NODE_SCHEMAS, schemaProperties } from "./nodeSchema";
import { SchemaForm } from "./SchemaForm";
import { nodeFieldHelp } from "./help";
import {
  changeMode,
  Fields,
  InactiveSettings,
  object,
  ReferenceSelect,
  type OptionsProps,
} from "./FormPresentation";

function routingChoices(
  extra: Record<string, unknown>,
  profile?: string,
  aliases = true,
) {
  const routing = object(extra.routing);
  const profiles = Object.keys(object(routing.profiles));
  const stores = Object.keys(object(routing.stores));
  const selected = object(object(routing.profiles)[profile ?? ""]);
  const environments = Object.entries(object(routing.environments)).flatMap(
    ([name, v]) => {
      const env = object(v);
      if (
        profile &&
        (!Array.isArray(env.profiles) ||
          !env.profiles.includes(profile) ||
          !Array.isArray(env.accounts) ||
          !env.accounts.includes(selected.account_id))
      )
        return [];
      return [
        name,
        ...(aliases && Array.isArray(env.aliases)
          ? env.aliases.map(String)
          : []),
      ];
    },
  );
  return { profiles, stores, environments };
}
export function HTTPOptionsForm(props: OptionsProps) {
  const { value, onChange, scope } = props;
  const extra = useGraphStore((s) => s.configExtra);
  const credentials = Object.keys(object(extra.credentials));
  const upstream = object(value.upstream);
  const admitted = value.upstream !== undefined;
  const routing = routingChoices(extra);
  const credential = object(value.credential);
  const managed = value.credential !== undefined;
  const perEnvironment = credential.environment_profiles !== undefined;
  const set = (key: string, v: unknown) => onChange({ ...value, [key]: v });
  const setMapping = (env: string, profile: string) => {
    const mappings = { ...object(credential.environment_profiles) };
    if (profile) mappings[env] = profile;
    else delete mappings[env];
    set("credential", { ...credential, environment_profiles: mappings });
  };
  const credentialProperties = schemaProperties(
    schemaProperties(NODE_SCHEMAS.http_request.schema).credential,
  );
  const environmentNames = routingChoices(
    extra,
    String(credential.routing_profile ?? ""),
    false,
  ).environments;
  return (
    <Stack gap="sm">
      <Select
        label="Target mode"
        value={admitted ? "upstream" : "url"}
        allowDeselect={false}
        data={[
          { value: "url", label: "URL or URL template" },
          { value: "upstream", label: "Backend selected by routing rules" },
        ]}
        onChange={(v) =>
          changeMode(
            value,
            ["url", "upstream"],
            v === "upstream"
              ? { upstream: { profile: "", environment_variable: "route" } }
              : { url: "" },
            onChange,
            scope,
          )
        }
      />
      <InactiveSettings
        value={value}
        fields={admitted ? ["url"] : ["upstream"]}
        onRemove={() =>
          changeMode(
            value,
            admitted ? ["url"] : ["upstream"],
            {},
            onChange,
            scope,
          )
        }
      />
      {admitted ? (
        <Fieldset legend="Backend from routing" p="sm">
          <Stack gap="sm">
            <ReferenceSelect
              label="Routing profile"
              value={upstream.profile}
              names={routing.profiles}
              panel="routing"
              onChange={(v) => set("upstream", { ...upstream, profile: v })}
            />
            <SchemaForm
              schema={{
                type: "object",
                properties: Object.fromEntries(
                  Object.entries(
                    schemaProperties(
                      schemaProperties(NODE_SCHEMAS.http_request.schema)
                        .upstream,
                    ),
                  ).filter(([k]) => k !== "profile"),
                ),
                required: ["environment_variable"],
              }}
              value={upstream}
              onChange={(v) => set("upstream", v)}
              scope={scope}
              path="upstream"
              fieldHelp={(f) => nodeFieldHelp("http_request", f)}
            />
          </Stack>
        </Fieldset>
      ) : (
        <Fields {...props} fields={["url"]} required={["url"]} />
      )}
      <Select
        label="HTTP method mode"
        value={value.forward_method ? "forward" : "fixed"}
        allowDeselect={false}
        data={[
          { value: "fixed", label: "Use a fixed method" },
          { value: "forward", label: "Forward incoming method" },
        ]}
        onChange={(v) =>
          changeMode(
            value,
            ["method", "forward_method"],
            v === "forward" ? { forward_method: true } : { method: "POST" },
            onChange,
            scope,
          )
        }
      />
      {!value.forward_method && (
        <Fields {...props} fields={["method"]} defaults={{ method: "POST" }} />
      )}
      {Boolean(value.forward_method) && value.method !== undefined && (
        <InactiveSettings
          value={value}
          fields={["method"]}
          onRemove={() => changeMode(value, ["method"], {}, onChange, scope)}
        />
      )}
      <Fields
        {...props}
        fields={["body", "timeout"]}
        defaults={{ body: "raw", timeout: "10s" }}
      />
      <Fieldset legend="Outbound credentials" p="sm">
        <Stack gap="sm">
          <Switch
            label="Use managed credentials"
            checked={managed}
            onChange={(e) =>
              changeMode(
                value,
                e.currentTarget.checked
                  ? ["credential", "retry", "redirects"]
                  : ["credential"],
                e.currentTarget.checked ? { credential: { profile: "" } } : {},
                onChange,
                scope,
              )
            }
          />
          {managed && (
            <>
              <Select
                label="Credential selection"
                value={perEnvironment ? "environment" : "fixed"}
                allowDeselect={false}
                data={[
                  { value: "fixed", label: "One credential profile" },
                  {
                    value: "environment",
                    label: "Credential profile per allowed environment",
                  },
                ]}
                onChange={(v) =>
                  set(
                    "credential",
                    v === "environment"
                      ? {
                          routing_profile: "",
                          environment_variable: "route",
                          environment_profiles: {},
                        }
                      : { profile: "" },
                  )
                }
              />
              {!perEnvironment ? (
                <ReferenceSelect
                  label="Credential profile"
                  names={credentials}
                  value={credential.profile}
                  panel="credentials"
                  onChange={(v) =>
                    set("credential", { ...credential, profile: v })
                  }
                />
              ) : (
                <>
                  <ReferenceSelect
                    label="Credential routing profile"
                    names={routing.profiles}
                    value={credential.routing_profile}
                    panel="routing"
                    onChange={(v) => {
                      const allowed = routingChoices(
                        extra,
                        v,
                        false,
                      ).environments;
                      set("credential", {
                        ...credential,
                        routing_profile: v,
                        environment_profiles: Object.fromEntries(
                          Object.entries(
                            object(credential.environment_profiles),
                          ).filter(([name]) => allowed.includes(name)),
                        ),
                      });
                    }}
                  />
                  <SchemaForm
                    schema={{
                      type: "object",
                      properties: {
                        environment_variable:
                          credentialProperties.environment_variable,
                      },
                      required: ["environment_variable"],
                    }}
                    value={credential}
                    onChange={(v) => set("credential", v)}
                    path="credential"
                    scope={scope}
                  />
                  {environmentNames.map((env) => (
                    <ReferenceSelect
                      key={env}
                      label={`Credential for ${env}`}
                      required={false}
                      names={credentials}
                      value={object(credential.environment_profiles)[env]}
                      panel="credentials"
                      onChange={(v) => setMapping(env, v)}
                    />
                  ))}
                  {Object.keys(object(credential.environment_profiles))
                    .filter((env) => !environmentNames.includes(env))
                    .map((env) => (
                      <Alert key={env} color="yellow">
                        Imported mapping {env} is not allowed by this routing
                        profile. It is retained.{" "}
                        <ReferenceSelect
                          label={`Imported credential for ${env}`}
                          value={object(credential.environment_profiles)[env]}
                          names={credentials}
                          panel="credentials"
                          onChange={(v) => setMapping(env, v)}
                        />
                        <Button
                          size="xs"
                          variant="light"
                          onClick={() => setMapping(env, "")}
                        >
                          Remove mapping {env}
                        </Button>
                      </Alert>
                    ))}
                  {Object.keys(object(credential.environment_profiles))
                    .length === 0 && (
                    <Text size="xs" c="red">
                      Choose a credential for at least one allowed environment.
                    </Text>
                  )}
                  {!environmentNames.length && (
                    <Text size="sm" c="dimmed">
                      Configure allowed environments for this routing profile
                      in Routing.
                    </Text>
                  )}
                </>
              )}
              <InactiveSettings
                value={credential}
                fields={
                  perEnvironment
                    ? ["profile"]
                    : [
                        "environment_profiles",
                        "routing_profile",
                        "environment_variable",
                      ]
                }
                onRemove={() => {
                  const next = { ...credential };
                  for (const key of perEnvironment
                    ? ["profile"]
                    : [
                        "environment_profiles",
                        "routing_profile",
                        "environment_variable",
                      ])
                    delete next[key];
                  set("credential", next);
                }}
              />
            </>
          )}
        </Stack>
      </Fieldset>
      {managed &&
        (value.retry !== undefined || value.redirects !== undefined) && (
          <InactiveSettings
            value={value}
            fields={["retry", "redirects"]}
            onRemove={() =>
              changeMode(value, ["retry", "redirects"], {}, onChange, scope)
            }
          />
        )}
      <Fields
        {...props}
        fields={[
          "forward_query",
          ...(value.forward_query ? ["query_mode", "remove_query"] : []),
          "header_mode",
          "forward_headers",
          "headers",
          "remove_headers",
          "body_methods",
          "remove_body_fields",
          "require_headers",
          "require_body_fields",
          "max_response_bytes",
          "response_bindings",
          "error_on_status",
          "allow_private_targets",
          ...(!managed ? ["retry", "redirects"] : []),
        ]}
        groupObjects
      />
    </Stack>
  );
}
export function RoutingOptionsForm(props: OptionsProps) {
  const { value, onChange, scope, nodeType } = props;
  const extra = useGraphStore((s) => s.configExtra);
  const choices = routingChoices(extra, String(value.profile ?? ""));
  const set = (key: string, v: unknown) => onChange({ ...value, [key]: v });
  if (nodeType === "register_route")
    return (
      <Stack gap="sm">
        <Select
          label="Registration mode"
          required
          value={String(value.mode ?? "")}
          allowDeselect={false}
          data={[
            { value: "after_create", label: "After upstream creation" },
            { value: "direct", label: "Direct registration" },
          ]}
          onChange={(v) =>
            changeMode(
              value,
              v === "after_create"
                ? ["require_headers", "id_source"]
                : ["id_source"],
              {
                mode: v ?? "direct",
                id_source: v === "after_create" ? "upstream" : "body",
              },
              onChange,
              scope,
            )
          }
        />
        <ReferenceSelect
          label="Routing profile"
          value={value.profile}
          names={choices.profiles}
          panel="routing"
          onChange={(v) => set("profile", v)}
        />
        <ReferenceSelect
          label="Routing store"
          value={value.store}
          names={choices.stores}
          panel="routing"
          onChange={(v) => set("store", v)}
        />
        <Fields
          {...props}
          fields={[
            "environment_variable",
            "id_source",
            "id_path",
            "id_type",
            ...(value.mode === "direct" ? ["require_headers"] : []),
            "pattern",
            "max_attempts",
            "timeout",
            "backoff",
          ]}
          required={["environment_variable", "id_source", "id_path", "id_type"]}
          override={{
            id_source: {
              type: "string",
              enum:
                value.mode === "after_create"
                  ? ["upstream"]
                  : ["body", "variable"],
            },
          }}
        />
        {value.mode === "after_create" && (
          <Text size="sm" c="dimmed">
            In the preceding Call an HTTP API node, capture the created identifier
            under response_bindings.fields. Enter that binding name here; the
            route is saved only after a 200 or 201 API response.
          </Text>
        )}
        {value.mode === "after_create" &&
          value.require_headers !== undefined && (
            <InactiveSettings
              value={value}
              fields={["require_headers"]}
              onRemove={() =>
                changeMode(value, ["require_headers"], {}, onChange, scope)
              }
            />
          )}
      </Stack>
    );
  const mode =
    value.fixed !== undefined
      ? "fixed"
      : value.store !== undefined
        ? "store"
        : "candidate";
  const inactive = mode === "fixed" ? ["store", "candidates"] : ["fixed"];
  const candidate = schemaProperties(NODE_SCHEMAS.route.schema).candidates;
  const item = object(candidate).items;
  const properties = schemaProperties(item as never);
  return (
    <Stack gap="sm">
      <ReferenceSelect
        label="Routing profile"
        value={value.profile}
        names={choices.profiles}
        panel="routing"
        onChange={(v) => set("profile", v)}
      />
      <Select
        label="Routing mode"
        value={mode}
        allowDeselect={false}
        data={[
          { value: "fixed", label: "Always use one allowed environment" },
          { value: "candidate", label: "Environment from request" },
          { value: "store", label: "Lookup identifier in a routing store" },
        ]}
        onChange={(v) =>
          changeMode(
            value,
            ["fixed", "candidates", "store"],
            v === "fixed"
              ? { fixed: "" }
              : v === "store"
                ? {
                    store: "",
                    candidates: [{ source: "body", path: "", id_type: "" }],
                  }
                : { candidates: [{ source: "body", path: "" }] },
            onChange,
            scope,
          )
        }
      />
      <InactiveSettings
        value={value}
        fields={inactive}
        onRemove={() => changeMode(value, inactive, {}, onChange, scope)}
      />
      {mode === "fixed" ? (
        <ReferenceSelect
          label="Allowed environment"
          value={value.fixed}
          names={choices.environments}
          panel="routing"
          onChange={(v) => set("fixed", v)}
        />
      ) : (
        <>
          {mode === "store" && (
            <ReferenceSelect
              label="Routing store"
              value={value.store}
              names={choices.stores}
              panel="routing"
              onChange={(v) => set("store", v)}
            />
          )}
          <Fields
            {...props}
            fields={["candidates"]}
            required={["candidates"]}
            override={{
              candidates: {
                type: "array",
                minItems: 1,
                maxItems: 16,
                items: {
                  type: "object",
                  properties: Object.fromEntries(
                    Object.entries(properties).filter(
                      ([k]) => mode === "store" || k !== "id_type",
                    ),
                  ),
                  required:
                    mode === "store"
                      ? ["source", "path", "id_type"]
                      : ["source", "path"],
                },
              },
            }}
          />
        </>
      )}
      <Fields {...props} fields={["name"]} defaults={{ name: "route" }} />
    </Stack>
  );
}
