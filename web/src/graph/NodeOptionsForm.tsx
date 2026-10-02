import { Alert, Button, Stack } from "@mantine/core";
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
import { SimpleOptionsForm, SIMPLE_NODE_TYPES } from "./SimpleOptionsForm";
import { NODE_SCHEMAS, schemaProperties } from "./nodeSchema";

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
  if (nodeType === "auth") return <AuthOptionsForm {...props} />;
  if (
    ["transform", "envelope", "batch", "filter", "if_else", "switch"].includes(
      nodeType,
    )
  )
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
  if (nodeType === "publish") return <PublishOptionsForm {...props} />;
  if (nodeType === "respond") return <RespondOptionsForm {...props} />;
  if (nodeType === "http_request") return <HTTPOptionsForm {...props} />;
  if (nodeType === "route" || nodeType === "register_route")
    return <RoutingOptionsForm {...props} />;
  if (nodeType === "no_auth")
    return (
      <Alert color="yellow" title="Public access">
        No options required. Connect Read request body before this node. Requests continue without checking the sender’s identity.
      </Alert>
    );
  if (SIMPLE_NODE_TYPES.includes(nodeType))
    return <SimpleOptionsForm {...props} />;
  return (
    <Stack gap="sm">
      {nodeType === "lookup" && (
        <Alert color="yellow" title="Provider configuration">
          Provider names depend on the runtime's registered lookup providers.
          Choose a provider your deployment supports.
        </Alert>
      )}
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
