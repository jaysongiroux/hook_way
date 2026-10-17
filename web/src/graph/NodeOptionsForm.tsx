import { MCPOptionsForm } from "./MCPOptionsForm";
import { Alert, Button, Stack, Text } from "@mantine/core";
import { AuthOptionsForm } from "./AuthOptionsForm";
import { PublishOptionsForm, RespondOptionsForm } from "./DeliveryOptionsForm";
import { HTTPOptionsForm, RoutingOptionsForm } from "./ProxyOptionsForm";
import { ProcessingOptionsForm } from "./ProcessingOptionsForm";
import {
  changeMode,
  Fields,
  WorkspaceActions,
  type OptionsProps,
} from "./FormPresentation";
import { useContext } from "react";
import { SimpleOptionsForm } from "./SimpleOptionsForm";
import { nodeDescriptor } from "./nodeDescriptors";
import { NODE_SCHEMAS, schemaProperties } from "./nodeSchema";
import { JSONValidationOptions } from "./JSONValidationOptions";
import { LaunchDarklyForm } from "./LaunchDarklyForm";
import { ProtectionOptionsForm } from "./ProtectionOptionsForm";

export function NodeOptionsForm(props: OptionsProps) {
  const known = Object.keys(
    schemaProperties(NODE_SCHEMAS[props.nodeType]?.schema),
  );
  const unknown = Object.keys(props.value).filter((k) => !known.includes(k));
  return (
    <Stack gap="sm">
      {unknown.length > 0 && (
        <Alert color="yellow" title="Unrecognized imported options">
          <Stack gap="xs">
            These values are retained: {unknown.join(", ")}. Review the YAML or
            remove them explicitly.
            <Button
              size="xs"
              variant="light"
              onClick={() =>
                changeMode(
                  props.value,
                  unknown,
                  {},
                  props.onChange,
                  props.scope,
                )
              }
            >
              Remove unrecognized options
            </Button>
          </Stack>
        </Alert>
      )}
      <NodeOptionsFields {...props} />
    </Stack>
  );
}
function NodeOptionsFields(props: OptionsProps) {
  const open = useContext(WorkspaceActions);
  const { nodeType } = props;
 if (nodeType === "launchdarkly") return <LaunchDarklyForm {...props} />;
  const editor = nodeDescriptor(nodeType)?.editor ?? "schema";
  if (editor === "mcp") return <MCPOptionsForm {...props} />;
  if (editor === "auth") return <AuthOptionsForm {...props} />;
  if (editor === "protection") return <ProtectionOptionsForm {...props} />;
  if (editor === "json-validation")
    return <JSONValidationOptions {...props} schemaField="schema" />;
  if (editor === "processing")
    return (
      <ProcessingOptionsForm
        {...props}
        onManageRouting={() => open("routing")}
        renderNodeOptions={(type, value, onChange, scope) => (
          <NodeOptionsForm
            nodeType={type}
            value={value}
            onChange={onChange}
            scope={scope}
            nodeId={String(value.id ?? props.nodeId ?? "")}
          />
        )}
      />
    );
  if (editor === "publish") return <PublishOptionsForm {...props} />;
  if (editor === "respond") return <RespondOptionsForm {...props} />;
  if (editor === "http") return <HTTPOptionsForm {...props} />;
  if (editor === "routing") return <RoutingOptionsForm {...props} />;
  if (editor === "public")
    return (
      <Alert color="yellow" title="Public access">
        No options required. Connect Read request body before this node. Requests continue without checking the sender’s identity.
      </Alert>
    );
  if (nodeType === "inspect")
    return (
      <Stack gap="xs">
        <Text size="sm">No node options. Configure the global Replay service connection to collect inspections, even when failure capture is disabled.</Text>
        <Text size="sm" c="dimmed">Passes the webhook onward, or ends an observation branch when unconnected. Place Sample → Inspect on a separate branch before Publish to spot-check traffic while every webhook is delivered.</Text>
        <Text size="xs" c="dimmed">Body and variables are captured as authored. Header policies and credential redactions apply. Uploads are best effort; inspections cannot be replayed.</Text>
      </Stack>
    );
  if (editor === "simple") return <SimpleOptionsForm {...props} />;
  return (
    <Stack gap="sm">
      <Fields
        {...props}
        fields={Object.keys(schemaProperties(NODE_SCHEMAS[nodeType]?.schema))}
        required={
          typeof NODE_SCHEMAS[nodeType]?.schema === "object"
            ? NODE_SCHEMAS[nodeType].schema.required
            : []
        }
        groupObjects
      />
    </Stack>
  );
}
