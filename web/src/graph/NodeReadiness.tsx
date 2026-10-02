import { Alert, Stack, Text } from "@mantine/core";
import { locateIssue, useBuilderValidation } from "./BuilderValidation";
import { nodeScope } from "./formDrafts";
import { useGraphStore } from "./store";
export function NodeReadiness({ nodeId }: { nodeId: string }) {
  const store = useGraphStore();
  const validation = useBuilderValidation();
  const issues = validation.issues.filter(
    (i) => locateIssue(i.field, store)?.nodeId === nodeId,
  );
  const scope = nodeScope(store.activeFlow, nodeId);
  const drafts = validation.draftIssues.filter(
    (i) => i.key.startsWith(scope + "|") || i.key.startsWith(scope + "/"),
  );
  if (validation.status === "checking")
    return (
      <Text size="xs" role="status">
        Checking configuration…
      </Text>
    );
  return (
    <Stack gap="xs">
      {drafts.length > 0 && (
        <Alert color="red" title="Unresolved edits">
          {drafts.map((d) => (
            <Text key={d.key} size="sm">
              {d.error}
            </Text>
          ))}
          Correct or revert these drafts before export.
        </Alert>
      )}
      {issues.length > 0 && (
        <Alert color="red" title="Node needs configuration">
          {issues.map((i, k) => (
            <Text size="sm" key={k}>
              {i.message}
              {i.suggestion ? ` ${i.suggestion}` : ""}
            </Text>
          ))}
        </Alert>
      )}
      {validation.status === "unavailable" && (
        <Text size="xs" c="dimmed">
          Configuration validation could not run. You can still edit fields;
          run hookway validate before deployment.
        </Text>
      )}
      {!issues.length && validation.status === "invalid" && (
        <Text size="xs" c="dimmed">
          The configuration has other errors. Export shows the full list.
        </Text>
      )}
      {validation.status === "valid" && !drafts.length && (
        <Text size="xs" c="teal">
          Configuration checks pass. Validate with the CLI before deploying.
        </Text>
      )}
    </Stack>
  );
}
