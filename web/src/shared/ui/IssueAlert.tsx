import { Alert, Button, List, Text } from "@mantine/core";

interface ValidationIssue {
  source?: string;
  field: string;
  message: string;
  suggestion?: string;
}

export function IssueAlert({
  color,
  title,
  issues,
  testId,
  action,
}: {
  action?: (
    issue: ValidationIssue,
  ) => { label: string; onClick: () => void } | undefined;
  color: "red" | "yellow";
  title: string;
  issues: ValidationIssue[];
  testId: string;
}) {
  return (
    <Alert color={color} title={title} data-testid={testId}>
      <List size="sm">
        {issues.map((issue, index) => {
          const issueAction = action?.(issue);
          return (
            <List.Item key={`${issue.field}-${index}`}>
              <Text span ff="monospace">
                {issue.source ? `${issue.source}: ` : ""}{issue.field}
              </Text>
              : {issue.message}
              {issue.suggestion && <Text size="sm">{issue.suggestion}</Text>}
              {issueAction && (
                <Button
                  size="compact-xs"
                  variant="subtle"
                  onClick={issueAction.onClick}
                >
                  {issueAction.label}
                </Button>
              )}
            </List.Item>
          );
        })}
      </List>
    </Alert>
  );
}
