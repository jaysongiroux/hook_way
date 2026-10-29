import { Alert, Fieldset, Select, Stack, Text } from "@mantine/core";
import { useGraphStore } from "./store";
import { schemaProperties, NODE_SCHEMAS, type JSONSchema } from "./nodeSchema";
import { clearDraftBranch } from "./formDrafts";
import { SchemaForm } from "./SchemaForm";
import { nodeFieldHelp } from "./help";
import { WebhookDeliveryNotice } from "./WebhookDeliveryNotice";
import { destinationPresentation } from "../shared/ui/destinationIdentity";
import {
  changeMode,
  Fields,
  InactiveSettings,
  object,
  ReferenceSelect,
  type Options,
  type OptionsProps,
} from "./FormPresentation";

const BROKER_FIELDS = [
  "group_id",
  "sqs",
  "sns",
  "resource_attributes",
  "wire_body",
  "fifo",
  "retry",
];
export function PublishOptionsForm(props: OptionsProps) {
  const { value, onChange, scope } = props;
  const destinations = useGraphStore((s) => s.destinations);
  const type = String(
    object(destinations[String(value.destination ?? "")]).type ?? "",
  );
  const broker = type === "sqs" || type === "sns";
  const fifo = String(value.target ?? "").endsWith(".fifo");
  const names = Object.keys(destinations);
  const brokerInactive = broker
    ? [
        ...(type === "sqs" ? ["sns"] : ["sqs"]),
        ...(fifo ? ["group_id"] : ["fifo"]),
      ]
    : BROKER_FIELDS.filter((key) => type !== "webhook" || key !== "wire_body");
  brokerInactive.push(...((type === "temporal" || type === "mcp_events") ? ["create"] : ["temporal"]));
  const schema = schemaProperties(NODE_SCHEMAS.publish.schema);
  const set = (key: string, v: unknown) => onChange({ ...value, [key]: v });
  const changeDestination = (destination: string) => {
    const nextType = String(object(destinations[destination]).type ?? "");
    changeMode(
      value,
      [
        ...(nextType !== "sqs" && nextType !== "sns"
          ? BROKER_FIELDS
          : nextType === "sqs"
            ? ["sns"]
            : ["sqs"]),
        ...(nextType !== type ? ["resource_attributes"] : []),
        ...(nextType !== "temporal" ? ["temporal"] : []),
        ...(!["s3", "redis_kv"].includes(nextType) ? ["payload"] : []),
      ],
      { destination },
      onChange,
      scope,
    );
  };
  const fifoValue = object(value.fifo);
  return (
    <Stack gap="sm">
      <ReferenceSelect
        label="Destination"
        value={value.destination}
        names={names}
        onChange={changeDestination}
        panel="destinations"
      />
      <Text size="sm" fw={600}>
        {type
          ? `Sends to ${destinationPresentation(type).label}`
          : "Choose a destination to see where this node sends its output."}
      </Text>
      <Fields
        {...props}
        fields={(type === "temporal" || type === "mcp_events") ? ["target"] : ["target", "create"]}
        required={type === "webhook" ? [] : ["target"]}
        labels={{
          target:
            type === "temporal"
              ? "Task queue"
              : type === "sqs"
                ? "Queue name"
                : type === "sns"
                  ? "Topic name"
                  : type === "s3"
                    ? "Object key"
                    : type === "webhook"
                      ? "Target path"
                      : "Target",
        }}
      />
      {type === "webhook" && (
        <Stack gap="xs">
          <Text size="xs" c="dimmed">
            This path is appended to the destination base URL. Leave it empty to
            use the exact URL configured in Destinations.
          </Text>
          <Select label="Webhook payload"
            description="JSON sends the current body, including a named event. Destination retry settings still apply. Proxy authentication forwards the original request instead."
            value={String(value.wire_body ?? "envelope")} allowDeselect={false}
            data={[{value: "envelope", label: "Hookway envelope"}, {value: "json", label: "Current JSON body"},
              ...(!["envelope", "json", ""].includes(String(value.wire_body ?? "")) ? [{value: String(value.wire_body), label: `Imported: ${String(value.wire_body)}`}] : [])]}
            onChange={(next) => set("wire_body", next || "envelope")} />
          <WebhookDeliveryNotice
            deliveryMode={
              object(
                object(destinations[String(value.destination ?? "")]).webhook,
              ).delivery_mode
            }
          />
        </Stack>
      )}
      <InactiveSettings
        value={value}
        fields={brokerInactive}
        onRemove={() => changeMode(value, brokerInactive, {}, onChange, scope)}
      />
      {broker && (
        <>
          <Select
            label="Queue / topic behavior"
            description="FIFO resources use a name ending in .fifo. Standard SQS queues and SNS topics remain fully supported."
            value={fifo ? "fifo" : "standard"}
            allowDeselect={false}
            data={[
              { value: "standard", label: "Standard" },
              { value: "fifo", label: "FIFO" },
            ]}
            onChange={(v) => {
              const target = String(value.target ?? "").replace(/\.fifo$/, "");
              changeMode(
                value,
                v === "fifo" ? ["group_id"] : ["fifo"],
                {
                  target: v === "fifo" ? `${target}.fifo` : target,
                  ...(v !== "fifo" && value.resource_attributes
                    ? {
                        resource_attributes: Object.fromEntries(
                          Object.entries(
                            object(value.resource_attributes),
                          ).filter(
                            ([k]) =>
                              ![
                                "DeduplicationScope",
                                "FifoThroughputLimit",
                                "FifoThroughputScope",
                                "ArchivePolicy",
                              ].includes(k),
                          ),
                        ),
                      }
                    : {}),
                  ...(v === "fifo"
                    ? {
                        fifo: {
                          group_id: { source: "static", value: "" },
                          content_based_deduplication: true,
                        },
                        ...(value[type] !== undefined
                          ? {
                              [type]: Object.fromEntries(
                                Object.entries(object(value[type])).filter(
                                  ([k]) =>
                                    k !==
                                    (type === "sqs"
                                      ? "delay_seconds"
                                      : "subject"),
                                ),
                              ),
                            }
                          : {}),
                      }
                    : {}),
                },
                onChange,
                scope,
              );
            }}
          />
          <Fields
            {...props}
            fields={["wire_body"]}
            defaults={{ wire_body: "envelope" }}
          />
          {fifo ? (
            <Fieldset legend="FIFO identity" p="sm">
              <Stack gap="sm">
                <IdentityEditor
                  label="Message group"
                  value={fifoValue.group_id}
                  onChange={(v) => set("fifo", { ...fifoValue, group_id: v })}
                  scope={scope}
                  path="fifo.group_id"
                  schema={schemaProperties(schema.fifo).group_id}
                />
                <Select
                  label="Deduplication"
                  value={
                    fifoValue.content_based_deduplication
                      ? "content"
                      : "identity"
                  }
                  allowDeselect={false}
                  data={[
                    { value: "content", label: "Content-based deduplication" },
                    {
                      value: "identity",
                      label: "Explicit deduplication identity",
                    },
                  ]}
                  onChange={(v) => {
                    const next = { ...fifoValue };
                    delete next.deduplication_id;
                    delete next.content_based_deduplication;
                    set(
                      "fifo",
                      v === "content"
                        ? { ...next, content_based_deduplication: true }
                        : {
                            ...next,
                            deduplication_id: { source: "raw_sha256" },
                          },
                    );
                  }}
                />
                {!fifoValue.content_based_deduplication && (
                  <IdentityEditor
                    label="Deduplication identity"
                    value={fifoValue.deduplication_id}
                    onChange={(v) =>
                      set("fifo", { ...fifoValue, deduplication_id: v })
                    }
                    scope={scope}
                    path="fifo.deduplication_id"
                    schema={schemaProperties(schema.fifo).deduplication_id}
                  />
                )}
                {Boolean(fifoValue.content_based_deduplication) &&
                  fifoValue.deduplication_id !== undefined && (
                    <InactiveSettings
                      value={fifoValue}
                      fields={["deduplication_id"]}
                      onRemove={() => {
                        const next = { ...fifoValue };
                        delete next.deduplication_id;
                        set("fifo", next);
                      }}
                    />
                  )}
              </Stack>
            </Fieldset>
          ) : (
            <>
              <Fields {...props} fields={[]} />
              <OptionalIdentity {...props} schema={schema.group_id} />
            </>
          )}
          <Fields
            {...props}
            fields={[
              type,
              "resource_attributes",
              "message_attributes",
              "retry",
            ]}
            groupObjects
            defaults={{
              "retry.initial_backoff": "100ms",
              "retry.max_backoff": "1s",
            }}
            override={{
              [type]: filteredBrokerSchema(schema[type], type, fifo),
            }}
          />
          {fifo && (
            <InactiveSettings
              value={object(value[type])}
              fields={[type === "sqs" ? "delay_seconds" : "subject"]}
              onRemove={() => {
                const next = { ...object(value[type]) };
                delete next[type === "sqs" ? "delay_seconds" : "subject"];
                set(type, next);
              }}
            />
          )}
          <Text size="xs" c="dimmed">
            Resource attributes configure queue or topic creation. Cross-account
            bindings require Create to be off. Content-based deduplication must
            also be enabled on the resource.
          </Text>
        </>
      )}
      {type === "temporal" && <TemporalOptions {...props} />}
      {!broker && type !== "temporal" && (
        <Fields
          {...props}
          fields={[
            ...(["s3", "redis_kv"].includes(type) ? ["payload"] : []),
            "message_attributes",
          ]}
          groupObjects
        />
      )}
    </Stack>
  );
}
function TemporalOptions(props: OptionsProps) {
  const value = object(props.value.temporal);
  const schema = schemaProperties(NODE_SCHEMAS.publish.schema).temporal;
  const id = object(value.workflow_id);
  const source = String(id.source ?? "");
  const idSchema = schemaProperties(schema).workflow_id;
  const idFields = [
    "source",
    "prefix",
    ...(source === "static"
      ? ["value"]
      : ["body", "header"].includes(source)
        ? ["path"]
        : []),
  ];
  const update = (next: Options) =>
    props.onChange({ ...props.value, temporal: next });
  return (
    <Fieldset legend="Temporal workflow" p="sm">
      <Stack gap="sm">
        <Text size="sm">
          Success means Temporal accepted the workflow start. A worker must poll
          this task queue; workflow completion happens after the webhook
          response.
        </Text>
        <SchemaForm
          schema={schema}
          value={value}
          onChange={(next) => update(object(next))}
          scope={props.scope}
          path="temporal"
          hideFields={["workflow_id"]}
          order={[
            "workflow_type",
            "input",
            "id_reuse_policy",
            "execution_timeout",
            "run_timeout",
            "task_timeout",
          ]}
          labels={{
            workflow_type: "Workflow type",
            input: "Workflow input",
            id_reuse_policy: "Workflow ID reuse policy",
            execution_timeout: "Total execution timeout",
            run_timeout: "Single run timeout",
            task_timeout: "Workflow task timeout",
          }}
          fieldHelp={(path) =>
            ({
              workflow_type:
                "Exact workflow type registered by your Temporal worker.",
              input:
                "Envelope includes Hookway metadata and the event. Body passes the decoded JSON event body; non-JSON falls back to the envelope.",
              id_reuse_policy:
                "Controls completed workflows with the same ID. Reject duplicate protects a stable provider event ID; allow duplicate permits another run. Terminate if running can stop an existing workflow.",
              execution_timeout:
                "Maximum lifetime across retries and continue-as-new. Use a positive Go duration, such as 24h.",
              run_timeout: "Maximum duration of one workflow run, such as 1h.",
              task_timeout:
                "Maximum duration of a workflow task, up to 2m. Omitted uses Temporal's default.",
            } as Record<string, string>)[path.split(".").at(-1) ?? path]
          }
        />
        <Select
          label="Workflow ID source"
          value={source || "generated"}
          allowDeselect={false}
          description="Use a stable provider event ID to control duplicates. Omitted uses the Hookway event ID, retained on replay. New ingress requests have new event IDs."
          data={[
            { value: "generated", label: "Use the Hookway event ID" },
            { value: "header", label: "Request header" },
            { value: "body", label: "Event body field" },
            { value: "static", label: "Fixed ID" },
            { value: "endpoint", label: "Flow name" },
          ]}
          onChange={(next) => {
            if (!next) return;
            clearDraftBranch(props.scope, "temporal.workflow_id");
            const updated = { ...value };
            if (next === "generated") delete updated.workflow_id;
            else updated.workflow_id = { source: next };
            update(updated);
          }}
        />
        <SchemaForm
          schema={{
            type: "object",
            properties: Object.fromEntries(
              idFields
                .filter((key) => key !== "source")
                .map((key) => [key, schemaProperties(idSchema)[key]]),
            ),
          }}
          value={id}
          onChange={(next) => update({ ...value, workflow_id: next })}
          scope={props.scope}
          path="temporal.workflow_id"
          labels={{
            path:
              source === "header" ? "Event ID header" : "Event ID body path",
            value: "Fixed workflow ID",
            prefix: "Workflow ID prefix",
          }}
        />
      </Stack>
    </Fieldset>
  );
}
function filteredBrokerSchema(
  schema: JSONSchema | undefined,
  type: string,
  fifo: boolean,
): JSONSchema {
  const properties = { ...schemaProperties(schema) };
  if (fifo) delete properties[type === "sqs" ? "delay_seconds" : "subject"];
  return { type: "object", properties, additionalProperties: false };
}
function OptionalIdentity(props: OptionsProps & { schema: JSONSchema }) {
  const value = props.value.group_id;
  return (
    <Fieldset legend="Standard message group (optional)" p="sm">
      <Stack gap="xs">
        <Select
          label="Message grouping"
          value={value === undefined ? "none" : "identity"}
          allowDeselect={false}
          data={[
            { value: "none", label: "No message group" },
            { value: "identity", label: "Set a message group identity" },
          ]}
          onChange={(v) =>
            changeMode(
              props.value,
              ["group_id"],
              v === "identity"
                ? { group_id: { source: "static", value: "" } }
                : {},
              props.onChange,
              props.scope,
            )
          }
        />
        {value !== undefined && (
          <IdentityEditor
            label="Message group"
            value={value}
            onChange={(v) => props.onChange({ ...props.value, group_id: v })}
            scope={props.scope}
            path="group_id"
            schema={props.schema}
          />
        )}
      </Stack>
    </Fieldset>
  );
}
function IdentityEditor({
  label,
  value,
  onChange,
  scope,
  path,
  schema,
}: {
  label: string;
  value: unknown;
  onChange: (v: Options) => void;
  scope: string;
  path: string;
  schema: JSONSchema | undefined;
}) {
  const obj = object(value),
    source = String(obj.source ?? "");
  const base = schemaProperties(schema);
  const selected = [
    "source",
    ...(source === "static"
      ? ["value"]
      : ["body", "header", "variable"].includes(source)
        ? ["path", "fallback", "fallback_hash"]
        : []),
    ...(["raw_sha256", "json_js_sha256"].includes(source) || obj.fallback_hash
      ? ["hash_length"]
      : []),
  ];
  const subset: JSONSchema = {
    type: "object",
    properties: Object.fromEntries(selected.map((k) => [k, base[k]])),
    required: [
      "source",
      ...(source === "static"
        ? ["value"]
        : ["body", "header", "variable"].includes(source)
          ? ["path"]
          : []),
    ],
  };
  return (
    <Stack gap="xs">
      <Text fw={600} size="sm">
        {label}
      </Text>
      <SchemaForm
        schema={subset}
        value={obj}
        path={path}
        scope={scope}
        fieldHelp={(f) => nodeFieldHelp("publish", f)}
        onChange={(v) => {
          const next = object(v);
          if (next.source !== source)
            changeMode(
              next,
              ["value", "path", "fallback", "fallback_hash", "hash_length"],
              {},
              onChange,
              scope,
            );
          else onChange(next);
        }}
      />
      <InactiveSettings
        value={obj}
        fields={Object.keys(base).filter((k) => !selected.includes(k))}
        onRemove={() =>
          changeMode(
            obj,
            Object.keys(base).filter((k) => !selected.includes(k)),
            {},
            onChange,
            scope,
          )
        }
      />
    </Stack>
  );
}
export function RespondOptionsForm(props: OptionsProps) {
  const { value, onChange, scope } = props;
  const passthrough = value.passthrough === true;
  const inactive = passthrough
    ? ["status", "body"]
    : ["forward_set_cookie", "forward_headers"];
  return (
    <Stack gap="sm">
      <Select
        label="Response mode"
        value={passthrough ? "passthrough" : "fixed"}
        allowDeselect={false}
        data={[
          { value: "passthrough", label: "Return upstream response" },
          { value: "fixed", label: "Fixed response" },
        ]}
        onChange={(v) =>
          changeMode(
            value,
            [
              "status",
              "body",
              "passthrough",
              "forward_headers",
              "forward_set_cookie",
            ],
            v === "passthrough" ? { passthrough: true } : { status: 200 },
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
      <Fields
        {...props}
        fields={
          passthrough
            ? ["headers", "forward_headers", "forward_set_cookie"]
            : ["status", "headers", "body"]
        }
        required={passthrough ? [] : ["status"]}
      />
      {[204, 304].includes(Number(value.status)) && Boolean(value.body) && (
        <Alert color="red">Status 204 and 304 must have an empty body.</Alert>
      )}
    </Stack>
  );
}
