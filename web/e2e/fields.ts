import { expect, type Locator } from "@playwright/test";
import { humanLabel, isRecord } from "./examples";

function arrayOfObjects(value: unknown[]): boolean {
  return value.some((item) => isRecord(item));
}

function formatArray(value: unknown[]): string {
  return value.map(String).join(", ");
}

async function expectSwitch(
  root: Locator,
  label: string,
  checked: boolean,
  nth: number,
) {
  const control = root
    .getByRole("switch", { name: label, exact: true })
    .nth(nth);
  if (checked) await expect.soft(control).toBeChecked();
  else await expect.soft(control).not.toBeChecked();
}

async function expectLabeled(
  root: Locator,
  label: string,
  expected: string,
  nth: number,
) {
  const named = root
    .getByRole("textbox", { name: label, exact: true })
    .or(root.getByRole("spinbutton", { name: label, exact: true }))
    .or(root.getByRole("combobox", { name: label, exact: true }));
  const control =
    (await named.count()) > nth
      ? named.nth(nth)
      : root.getByLabel(new RegExp(`^${escape(label)}(?:\\s*\\*)?$`)).nth(nth);
  await expect(control).toBeVisible();
  const value = await control.inputValue().catch(() => "");
  const text = (await control.innerText().catch(() => "")).trim();
  const actual = value || text;
  expect.soft(actual, `field "${label}"`).toBe(expected);
}

/** Assert a YAML value as the builder's SchemaForm would render it. */
export async function expectYamlValue(
  root: Locator,
  key: string,
  value: unknown,
  nth = 0,
  labelOverride?: string,
): Promise<void> {
  if (value === undefined) return;
  const label = labelOverride ?? humanLabel(key);

  // Unconstrained schemas and inline child node definitions use a JSON editor.
  const jsonEditor = root.getByLabel(label, { exact: true });
  if (
    (await jsonEditor.count()) > nth &&
    (await jsonEditor.nth(nth).getAttribute("data-json-editor"))
  ) {
    await expect.soft(jsonEditor.nth(nth)).toBeVisible();
    expect
      .soft(
        JSON.parse(await jsonEditor.nth(nth).inputValue()),
        `JSON field "${label}"`,
      )
      .toEqual(value);
    return;
  }
  if (Array.isArray(value)) {
    if (arrayOfObjects(value)) {
      const group = root
        .getByRole("group", {
          name: new RegExp(`^${escape(label)}(?:\\s*\\*)?$`),
        })
        .nth(nth);
      await expect.soft(group).toBeVisible();
      for (let i = 0; i < value.length; i++) {
        const row = value[i];
        if (!isRecord(row)) continue;
        const rowJSON = group.getByLabel("Value", { exact: true }).nth(i);
        if (
          (await group.getByLabel("Value", { exact: true }).count()) > i &&
          (await rowJSON.getAttribute("data-json-editor"))
        ) {
          expect.soft(JSON.parse(await rowJSON.inputValue())).toEqual(row);
          continue;
        }
        for (const [childKey, childValue] of Object.entries(row)) {
          await expectYamlValue(
            group,
            childKey,
            childValue,
            i,
            key === "mappings"
              ? (
                  { from: "Source path", to: "Destination path" } as Record<
                    string,
                    string
                  >
                )[childKey]
              : key === "candidates"
                ? (
                    {
                      id_type: "Identifier type",
                      pattern: "Identifier pattern",
                    } as Record<string, string>
                  )[childKey]
                : undefined,
          );
        }
      }
      return;
    }
    await expectLabeled(root, label, formatArray(value), nth);
    return;
  }

  if (isRecord(value)) {
    const group = root
      .getByRole("group", {
        name: new RegExp(`^${escape(label)}(?:\\s*\\*)?$`),
      })
      .nth(nth);
    await expect.soft(group).toBeVisible();
    const mapKey = group.getByLabel("Key", { exact: true });
    // A structured object can contain a nested map whose Key input is also
    // found by this descendant locator. Only scalar-valued objects are maps
    // rendered by MapField; otherwise recurse into their own named groups.
    const scalarValues = Object.values(value).every(
      (entry) => entry === null || typeof entry !== "object",
    );
    if (scalarValues && (await mapKey.count()) > 0) {
      const entries = Object.entries(value);
      const inputs = group.getByRole("textbox");
      await expect.soft(inputs).toHaveCount(entries.length * 2);
      for (let i = 0; i < entries.length; i++) {
        await expect.soft(inputs.nth(i * 2)).toHaveValue(entries[i][0]);
        await expect
          .soft(inputs.nth(i * 2 + 1))
          .toHaveValue(String(entries[i][1] ?? ""));
      }
      return;
    }
    for (const [childKey, childValue] of Object.entries(value)) {
      await expectYamlValue(group, childKey, childValue, 0);
    }
    return;
  }

  if (typeof value === "boolean") {
    const switches = root.getByRole("switch", { name: label, exact: true });
    if ((await switches.count()) > nth) {
      await expectSwitch(root, label, value, nth);
      return;
    }
    await expectLabeled(root, label, String(value), nth);
    return;
  }

  await expectLabeled(root, label, String(value), nth);
}

function escape(text: string) {
  return text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

const AUTH_LABELS: Record<string, string> = {
  hmac_sha1_hex: "HMAC SHA-1 · hexadecimal",
  hmac_sha256_ts_colon: "Timestamp + HMAC SHA-256 · labeled header",
  exact: "Exact value",
  basic: "HTTP Basic",
  hmac_sha256_hex: "HMAC SHA-256 · hexadecimal",
  hmac_sha256_base64: "HMAC SHA-256 · Base64",
  hmac_sha512_hex: "HMAC SHA-512 · hexadecimal",
  hmac_sha512_base64: "HMAC SHA-512 · Base64",
  jwt_hs256: "JWT · HS256",
  jwt_rs256: "JWT · RS256",
  jwt_es256: "JWT · ES256",
  hmac_sha256_ts_dot: "Timestamp + HMAC SHA-256",
  hmac_sha256_ts_dot_multi: "Timestamp + multiple signatures",
  hmac_sha256_headers_dot: "Separate timestamp + HMAC SHA-256",
  ecdsa_p256_sha256_headers: "Separate timestamp + ECDSA P-256",
};

/** Mode-specific UI evidence is paired with exact whole-node YAML roundtrips. */
export async function expectNodeOptions(
  root: Locator,
  type: string,
  options: Record<string, unknown>,
  destinations: Record<string, unknown> = {},
) {
  const displayLabels: Record<string, Record<string, string>> = {
    transform: {
      from: "First value path",
      to: "Second value path",
      mappings: "Field mappings",
      fields: "Fields to remove",
      rules: "XML cleanup rules",
      json_schema: "JSON Schema",
    },
    batch: {
      environment_path: "Environment field path",
      event_id_path: "Event ID field path",
    },
    route: { candidates: "Fields to try for routing" },
    register_route: {
      id_source: "Identifier source",
      id_path: "Identifier field or binding",
    },
    publish: {
      create: "Create the queue or topic at startup",
      payload: "Payload to send",
      wire_body: "Message body format",
    },
    http_request: { url: "API URL", body: "Request body to send" },
    respond: { status: "HTTP status code", body: "Response body" },
  };
  const scalar = (key: string, label?: string) =>
    expectYamlValue(
      root,
      key,
      options[key],
      0,
      label ??
        displayLabels[type]?.[key] ??
        (
          { timeout: "Timeout", name: "Result variable" } as Record<
            string,
            string
          >
        )[key],
    );
  if (type === "auth") {
    const scheme = String(options.scheme ?? "");
    await expectLabeled(
      root,
      "Authentication strategy",
      AUTH_LABELS[scheme] ?? humanLabel(scheme),
      0,
    );
    if (options.secret_ref === undefined)
      await scalar("secret", "Verification key");
    else {
      const ref = isRecord(options.secret_ref) ? options.secret_ref : {};
      await expectLabeled(root, "Secret provider", String(ref.provider), 0);
      await expectYamlValue(
        root.getByRole("group", { name: /^Secret reference/ }),
        "name",
        ref.name,
      );
    }
    if (isRecord(options.source)) {
      await expectLabeled(
        root,
        "Credential location",
        options.source.kind === "query" ? "Query parameter" : "Request header",
        0,
      );
      await expectLabeled(
        root,
        options.source.kind === "query"
          ? "Query parameter name"
          : "Signature header name",
        String(options.source.key),
        0,
      );
    } else await scalar("signature_header", "Signature header name");
    if (scheme.startsWith("jwt_"))
      await scalar("body_hash_claim", "Raw body SHA-256 claim");
    if (options.timestamp_header !== undefined)
      await scalar("timestamp_header");
    if (options.timestamp_unit !== undefined) await scalar("timestamp_unit");
    return;
  }
  if (type === "filter" || type === "if_else") {
    if (type === "if_else")
      await expectLabeled(
        root,
        "Match conditions",
        String(options.match ?? "all"),
        0,
      );
    if (Array.isArray(options.conditions))
      for (let index = 0; index < options.conditions.length; index++)
        await expectCondition(
          root.getByRole("group", {
            name: `Condition ${index + 1}`,
            exact: true,
          }),
          options.conditions[index],
          `Condition ${index + 1} `,
        );
    if (type === "filter") {
      await scalar("on_no_match", "When conditions do not match");
      await scalar("drop_reason", "Drop reason");
    }
    return;
  }
  if (type === "switch") {
    if (Array.isArray(options.cases))
      for (let index = 0; index < options.cases.length; index++) {
        const entry = options.cases[index];
        if (!isRecord(entry)) continue;
        const group = root.getByRole("group", {
          name: `Case ${index + 1}`,
          exact: true,
        });
        await expectLabeled(
          group,
          `Case ${index + 1} output port`,
          String(entry.port),
          0,
        );
        await expectLabeled(
          group,
          `Case ${index + 1} condition mode`,
          entry.when !== undefined ? "single" : "list",
          0,
        );
        if (entry.when !== undefined)
          await expectCondition(
            group.getByRole("group", { name: "Condition", exact: true }),
            entry.when,
            "",
          );
        else if (Array.isArray(entry.conditions))
          for (let i = 0; i < entry.conditions.length; i++)
            await expectCondition(
              group.getByRole("group", {
                name: `Condition ${i + 1}`,
                exact: true,
              }),
              entry.conditions[i],
              `Condition ${i + 1} `,
            );
      }
    await scalar("default", "Default output port");
    return;
  }
  if (type === "json_validate" || (type === "transform" && options.kind === "json_schema")) {
    if (type === "transform")
      await expectLabeled(root, "Transform operation", "json_schema", 0);
    await expectYamlValue(root, type === "json_validate" ? "schema" : "json_schema",
      options[type === "json_validate" ? "schema" : "json_schema"], 0, "JSON Schema");
    if (Array.isArray(options.coercions)) {
      for (let i = 0; i < options.coercions.length; i++) {
        const rule = options.coercions[i];
        if (!isRecord(rule)) throw new Error("Expected a conversion object");
        await expect(root.getByLabel(`Property path ${i + 1}`, { exact: true })).toHaveValue(String(rule.path));
        await expect(root.getByLabel(`Convert to ${i + 1}`, { exact: true })).toHaveValue(String(rule.type));
      }
    }
    return;
  }
  if (type === "transform") {
    await expectLabeled(root, "Transform operation", String(options.kind), 0);
    const fields = String(options.kind).startsWith("changed_fields")
      ? ["from", "to", "name"]
      : options.kind === "field_map"
        ? ["mappings"]
        : options.kind === "strip_fields"
          ? ["fields"]
          : options.kind === "json_schema"
            ? ["json_schema"]
            : options.kind === "xml_prune"
              ? ["rules"]
              : [];
    for (const key of fields) await scalar(key);
    if (options.kind === "xml_ordered_prune" && isRecord(options.xml_adapter)) {
      const adapter = root.getByRole("group", {
        name: /^XML cleanup selectors/,
      });
      await expectYamlValue(
        adapter,
        "sequence_attribute",
        options.xml_adapter.sequence_attribute,
      );
    }
    return;
  }
  if (type === "envelope") {
    const format = String(options.body_format ?? "json");
    await expectLabeled(root, "Body format", format, 0);
    if (format === "xml" && isRecord(options.xml)) {
      for (const [key, label] of Object.entries({
        max_bytes: "Maximum XML bytes",
        max_depth: "Maximum element depth",
        max_nodes: "Maximum XML nodes",
      }))
        await expectYamlValue(root, key, options.xml[key], 0, label);
    }
    if (format === "multipart" && isRecord(options.multipart)) {
      for (const [key, label] of Object.entries({
        max_total_bytes: "Maximum total multipart bytes",
        max_parts: "Maximum parts",
        max_part_bytes: "Maximum bytes per part",
        include_content: "Include file content",
      }))
        await expectYamlValue(root, key, options.multipart[key], 0, label);
    }
    return;
  }
  if (type === "publish") {
    await scalar("destination", "Destination");
    const rawDestination = destinations[String(options.destination)];
    const destination = isRecord(rawDestination) ? rawDestination : {};
    const backend = String(destination.type ?? "");
    await scalar(
      "target",
      backend === "temporal"
        ? "Task queue"
        : backend === "sqs"
          ? "Queue name"
          : backend === "sns"
            ? "Topic name"
            : backend === "s3"
              ? "Object key"
              : backend === "webhook"
                ? "Target path"
                : "Target",
    );
    await scalar("create");
    if (backend === "temporal" && isRecord(options.temporal)) {
      for (const [key, label] of Object.entries({
        workflow_type: "Workflow type",
        input: "Workflow input",
        id_reuse_policy: "Workflow ID reuse policy",
        execution_timeout: "Total execution timeout",
        run_timeout: "Single run timeout",
        task_timeout: "Workflow task timeout",
      })) {
        if (options.temporal[key] !== undefined)
          await expectYamlValue(root, key, options.temporal[key], 0, label);
      }
    }

    if (backend === "sqs" || backend === "sns") {
      const fifo = String(options.target).endsWith(".fifo");
      await expectLabeled(
        root,
        "Queue / topic behavior",
        fifo ? "FIFO" : "Standard",
        0,
      );
      await scalar("wire_body");
      if (fifo && isRecord(options.fifo)) {
        const group = root.getByRole("group", {
          name: "FIFO identity",
          exact: true,
        });
        if (isRecord(options.fifo.group_id))
          await expectYamlValue(group, "source", options.fifo.group_id.source);
        await expectLabeled(
          group,
          "Deduplication",
          options.fifo.content_based_deduplication
            ? "Content-based deduplication"
            : "Explicit deduplication identity",
          0,
        );
      }
    } else if (backend === "s3" || backend === "redis_kv")
      await scalar("payload");
    return;
  }
  if (type === "http_request") {
    await expectLabeled(
      root,
      "Target mode",
      options.upstream !== undefined
        ? "Backend selected by routing rules"
        : "URL or URL template",
      0,
    );
    if (isRecord(options.upstream))
      await expectYamlValue(
        root.getByRole("group", { name: "Backend from routing", exact: true }),
        "profile",
        options.upstream.profile,
        0,
        "Routing profile",
      );
    else await scalar("url");
    await expectLabeled(
      root,
      "HTTP method mode",
      options.forward_method ? "Forward incoming method" : "Use a fixed method",
      0,
    );
    if (!options.forward_method) await scalar("method");
    await scalar("body");
    await scalar("timeout");
    if (
      isRecord(options.credential) &&
      options.credential.profile !== undefined
    )
      await expectYamlValue(
        root,
        "profile",
        options.credential.profile,
        0,
        "Credential profile",
      );
    return;
  }
  if (type === "route" || type === "register_route") {
    await scalar("profile", "Routing profile");
    if (type === "route") {
      await expectLabeled(
        root,
        "Routing mode",
        options.fixed !== undefined
          ? "Always use one allowed environment"
          : options.store !== undefined
            ? "Lookup identifier in a routing store"
            : "Environment from request",
        0,
      );
      if (options.fixed !== undefined)
        await scalar("fixed", "Allowed environment");
      else {
        if (options.store !== undefined) await scalar("store", "Routing store");
        await scalar("candidates");
      }
    } else {
      await expectLabeled(
        root,
        "Registration mode",
        options.mode === "after_create"
          ? "After upstream creation"
          : "Direct registration",
        0,
      );
      await scalar("store", "Routing store");
      await scalar("id_source");
      await scalar("id_path");
    }
    return;
  }
  if (type === "batch") {
    await expectLabeled(root, "Batch mode", String(options.mode), 0);
    await scalar("profile", "Routing profile");
    await scalar("environment_path");
    await scalar("event_id_path");
    if (options.mode === "dedicated")
      await scalar("receiving_environment", "Receiving environment");
    if (isRecord(options.child)) {
      await expectLabeled(
        root,
        "Child entry node",
        String(options.child.entry),
        0,
      );
      if (Array.isArray(options.child.nodes))
        for (let index = 0; index < options.child.nodes.length; index++) {
          const node = options.child.nodes[index];
          if (!isRecord(node)) continue;
          const group = root.getByRole("group", {
            name: `Child node ${index + 1}`,
            exact: true,
          });
          await expectLabeled(
            group,
            `Child node ${index + 1} ID`,
            String(node.id),
            0,
          );
          await expectLabeled(
            group,
            `Child node ${index + 1} type`,
            String(node.type),
            0,
          );
          const childOptions = { ...node };
          delete childOptions.id;
          delete childOptions.type;
          delete childOptions.description;
          await expectNodeOptions(
            group,
            String(node.type),
            childOptions,
            destinations,
          );
        }
    }
    return;
  }
  if (type === "respond") {
    await expectLabeled(
      root,
      "Response mode",
      options.passthrough === true
        ? "Return upstream response"
        : "Fixed response",
      0,
    );
    if (options.passthrough !== true) {
      await scalar("status");
      await scalar("body");
    }
    return;
  }
  const simple: Record<string, Record<string, string>> = {
    http_trigger: { path: "Request path" },
    read_body: {
      max_bytes: "Body read limit (bytes)",
      allow_empty: "allow empty",
    },
    ip_allowlist: { cidrs: "Allowed IPs and networks" },
    loop_guard: { max_hops: "Maximum forwarded hops" },
    replay_guard: { ttl: "Duplicate window", key: "Duplicate identity" },
    lookup: {
      provider: "Custom provider name",
      key: "Lookup key",
      name: "Result variable",
    },
    code: { script: "JavaScript function body" },
    rate_limit: { rpm: "Requests per minute", burst: "Burst allowance" },
    sample: { rate: "Sampling rate" },
  };
  if (simple[type]) {
    for (const [key, label] of Object.entries(simple[type]))
      await scalar(key, label);
    if (type === "http_trigger") {
      await expect(
        root.getByRole("textbox", {
          name: "Accepted HTTP methods",
          exact: true,
        }),
      ).toBeVisible();
      if (Array.isArray(options.methods))
        for (const method of options.methods)
          await expect(
            root.getByText(String(method), { exact: true }).first(),
          ).toBeVisible();
    }
    if (type === "rate_limit")
      await expectLabeled(
        root,
        "Rate-limit identity",
        String(options.key_by ?? "source_ip").startsWith("header.")
          ? "Request header"
          : options.key_by && options.key_by !== "source_ip"
            ? String(options.key_by)
            : "Source IP",
        0,
      );
    return;
  }
  if (type === "no_auth") {
    await expect(
      root.getByText(
        "No options required. Connect Read request body before this node. Requests continue without checking the sender’s identity.",
      ),
    ).toBeVisible();
    return;
  }
  for (const [key, value] of Object.entries(options))
    await expectYamlValue(root, key, value);
}

async function expectCondition(root: Locator, raw: unknown, prefix: string) {
  if (!isRecord(raw))
    throw new Error("Expected an object condition in the valid import fixture");
  await expectLabeled(root, `${prefix}Source`, String(raw.source), 0);
  await expectLabeled(
    root,
    `${prefix}${raw.source === "header" ? "Header name" : "Body path"}`,
    String(raw.path),
    0,
  );
  await expectLabeled(root, `${prefix}Operator`, String(raw.operator), 0);
  if (!["exists", "not_exists"].includes(String(raw.operator)))
    await expectLabeled(root, `${prefix}Value`, String(raw.value), 0);
}
