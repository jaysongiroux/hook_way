import { useState, type ReactNode } from "react";
import {
  Alert,
  Button,
  Group,
  Select,
  Stack,
  Text,
  TextInput,
} from "@mantine/core";
import { DARK } from "../tokens";

export function record(value: unknown): Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

export function routingNameError(name: string): string | undefined {
  return /^[a-z][a-z0-9-]{0,62}$/.test(name)
    ? undefined
    : "Start with a lowercase letter; use lowercase letters, digits or hyphens (up to 63 characters).";
}

export function credentialNameError(name: string): string | undefined {
  return name &&
    new TextEncoder().encode(name).length <= 1024 &&
    !/[\s\u0000-\u001f\u007f-\u009f:>]/u.test(name)
    ? undefined
    : "Use a nonempty name without whitespace, control characters, : or > (up to 1024 bytes).";
}

/** Named maps use explicit commits, so a duplicate draft cannot overwrite another entry. */
export function WorkspaceEntries({
  label,
  value,
  onChange,
  initial,
  nameError,
  children,
  warning,
}: {
  label: string;
  value: unknown;
  onChange: (next: Record<string, unknown>) => void;
  initial: () => Record<string, unknown>;
  nameError: (name: string) => string | undefined;
  children: (
    name: string,
    entry: Record<string, unknown>,
    change: (entry: Record<string, unknown>) => void,
  ) => ReactNode;
  warning?: string;
}) {
  const entries = record(value);
  const names = Object.keys(entries);
  const [picked, setPicked] = useState<string | null>(null);
  const [newName, setNewName] = useState("");
  const [attempted, setAttempted] = useState(false);
  const selected = picked && names.includes(picked) ? picked : names[0];
  const draft = newName.trim();
  const error = Object.prototype.hasOwnProperty.call(entries, draft)
    ? "That name already exists."
    : nameError(draft);
  return (
    <Stack gap="sm">
      {warning && (
        <Text size="sm" c={DARK.muted}>
          {warning}
        </Text>
      )}
      <Group align="flex-start" wrap="wrap">
        <TextInput
          label={`New ${label} name`}
          value={newName}
          error={attempted ? error : undefined}
          style={{ flex: 1, minWidth: 180 }}
          onChange={(event) => {
            setNewName(event.currentTarget.value);
            setAttempted(false);
          }}
        />
        <Button
          mt={24}
          onClick={() => {
            setAttempted(true);
            if (error) return;
            onChange({ ...entries, [draft]: initial() });
            setPicked(draft);
            setNewName("");
            setAttempted(false);
          }}
        >
          Add {label}
        </Button>
      </Group>
      {names.length === 0 ? (
        <Text size="sm" c={DARK.muted}>
          No {label}s configured. Add one above to make it available to nodes.
        </Text>
      ) : (
        <>
          <Select
            label={`Selected ${label}`}
            data={names}
            value={selected ?? null}
            allowDeselect={false}
            onChange={setPicked}
          />
          {selected && (
            <Stack key={selected} gap="sm">
              <EntryName
                name={selected}
                names={names}
                nameError={nameError}
                onRename={(next) => {
                  onChange(
                    Object.fromEntries(
                      Object.entries(entries).map(([name, entry]) => [
                        name === selected ? next : name,
                        entry,
                      ]),
                    ),
                  );
                  setPicked(next);
                }}
              />
              {typeof entries[selected] !== "object" ||
              entries[selected] === null ||
              Array.isArray(entries[selected]) ? (
                <Alert color="orange">
                  Imported entry is not an object. Remove it and add a new entry
                  to repair it; its original value is retained.
                </Alert>
              ) : (
                children(selected, record(entries[selected]), (entry) =>
                  onChange({ ...entries, [selected]: entry }),
                )
              )}
              <Button
                variant="subtle"
                color="red"
                onClick={() => {
                  const next = { ...entries };
                  delete next[selected];
                  onChange(next);
                  setPicked(null);
                }}
              >
                Remove {label} {selected}
              </Button>
            </Stack>
          )}
        </>
      )}
    </Stack>
  );
}

function EntryName({
  name,
  names,
  nameError,
  onRename,
}: {
  name: string;
  names: string[];
  nameError: (name: string) => string | undefined;
  onRename: (next: string) => void;
}) {
  const [draft, setDraft] = useState(name);
  const [submitted, setSubmitted] = useState(false);
  const next = draft.trim();
  const error =
    next !== name && names.includes(next)
      ? "That name already exists."
      : nameError(next);
  return (
    <Group align="flex-start">
      <TextInput
        label="Name"
        description="Renaming does not rewrite existing node references. Update those references after renaming."
        value={draft}
        style={{ flex: 1 }}
        error={submitted ? error : undefined}
        onChange={(e) => {
          setDraft(e.currentTarget.value);
          setSubmitted(false);
        }}
      />
      <Button
        variant="light"
        mt={24}
        disabled={next === name}
        onClick={() => {
          setSubmitted(true);
          if (!error) onRename(next);
        }}
      >
        Rename
      </Button>
    </Group>
  );
}
