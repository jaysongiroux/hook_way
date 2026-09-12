import { Box, Input, SegmentedControl, Select, Stack, Text, TextInput } from "@mantine/core";
import { useId } from "react";
import type { AuthData, EndpointData } from "./model";
import { AUTH_TYPES, SCHEMES_BY_AUTH, defaultScheme } from "./destTypes";

type Setter = (patch: Record<string, unknown>) => void;

// EndpointRequestFields edits how a request reaches this endpoint.
export function EndpointRequestFields({ data, set }: { data: EndpointData; set: Setter }) {
  return (
    <Stack gap="xs">
      <TextInput
        label="Path"
        description="Literal, or with whole-segment placeholders such as /webhooks/{envName}/digifi"
        value={data.path}
        onChange={(e) => set({ path: e.currentTarget.value })}
      />
    </Stack>
  );
}

// EndpointAuthFields edits signature verification for the endpoint. The
// secret-source toggle carries its own label: while unlabeled it sat directly
// above the Variables group and read as part of it.
export function EndpointAuthFields({ data, set }: { data: EndpointData; set: Setter }) {
  const auth = data.auth;
  const setAuth = (patch: Partial<AuthData>) => set({ auth: { ...auth, ...patch } });
  const secretSourceLabelId = useId();
  return (
    <Stack gap="xs">
      <Select
        label="Auth type"
        data={AUTH_TYPES}
        value={auth.type}
        allowDeselect={false}
        comboboxProps={{ keepMounted: false }}
        onChange={(v) => {
          const t = v ?? "none";
          setAuth({ type: t, scheme: defaultScheme(t) });
        }}
      />
      {(auth.type === "hmac_sha256" || auth.type === "jwt") && (
        <Select
          label="Scheme"
          data={SCHEMES_BY_AUTH[auth.type] ?? []}
          value={auth.scheme}
          allowDeselect={false}
          comboboxProps={{ keepMounted: false }}
          onChange={(v) => setAuth({ scheme: v ?? defaultScheme(auth.type) })}
        />
      )}
      {auth.type !== "none" && (
        <>
          <TextInput
            label="Signature header"
            value={auth.signatureHeader}
            onChange={(e) => setAuth({ signatureHeader: e.currentTarget.value })}
          />
          <Box role="group" aria-labelledby={secretSourceLabelId}>
            <Input.Label id={secretSourceLabelId} style={{ display: "block", marginBottom: 4 }}>
              Secret source
            </Input.Label>
            <SegmentedControl
              fullWidth
              size="xs"
              data={[
                { label: "Literal", value: "literal" },
                { label: "Env var", value: "env" },
              ]}
              value={auth.secretMode}
              onChange={(v) => setAuth({ secretMode: v as AuthData["secretMode"] })}
            />
          </Box>
          <TextInput
            label={auth.secretMode === "env" ? "Secret env var name" : "Secret (literal)"}
            placeholder={auth.secretMode === "env" ? "GITHUB_WEBHOOK_SECRET" : "raw secret value"}
            value={auth.secret}
            onChange={(e) => setAuth({ secret: e.currentTarget.value })}
          />
          {auth.secretMode === "literal" && (
            <Text size="xs" c="orange">
              A literal secret is written into exported YAML and the local cache.
            </Text>
          )}
        </>
      )}
    </Stack>
  );
}
