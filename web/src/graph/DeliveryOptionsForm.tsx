import { Alert, Fieldset, Select, Stack, Text } from "@mantine/core";
import { useGraphStore } from "./store";
import { schemaProperties, NODE_SCHEMAS, type JSONSchema } from "./nodeSchema";
import { SchemaForm } from "./SchemaForm";
import { nodeFieldHelp } from "./help";
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
    : BROKER_FIELDS;
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
      <Fields
        {...props}
        fields={["target", "create"]}
        required={["target"]}
        labels={{
          target:
            type === "sqs"
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
        <Text size="xs" c="dimmed">
          This path is appended to the destination base URL. Configure the base
          URL in Destinations.
        </Text>
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
      {!broker && (
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
