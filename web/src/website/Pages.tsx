import { Anchor, Button, Group, List, Stack, Text, Title } from "@mantine/core";
import { Link } from "react-router-dom";
import { WebsitePage } from "./Page";
import { GITHUB_REPO, docsHref } from "../marketing/links";
import { CtaBand } from "../marketing/CtaBand";
import { useAnalyticsPreferences } from "./WebsiteAnalytics";

export function MCPPage() {
  return (
    <WebsitePage
      title="Expose selected APIs as authenticated MCP tools"
      lead="Hookway is a self-hosted API-to-MCP gateway. Import selected OpenAPI operations or build native tool branches, review the YAML, and run the endpoint in your infrastructure."
    >
      <Group mb={40}>
        <Button component={Link} to={docsHref("docs/guides/openapi-import.md")}>
          Follow the API-to-MCP guide
        </Button>
        <Button component={Link} to="/builder" variant="default">
          Try the builder
        </Button>
      </Group>
      <Stack gap="xl">
        <section>
          <Title order={2}>Choose the operations an agent can call</Title>
          <Text mt="sm">
            Import an OpenAPI 3.0 or 3.1 document locally. Select supported
            operations, review input and output schemas, and grant each caller
            only the tools it needs. Writes require an explicit mutation grant.
            Importing does not deploy or merge a configuration.
          </Text>
        </section>
        <section>
          <Title order={2}>Keep caller and API credentials separate</Title>
          <Text mt="sm">
            Authenticate MCP callers with API keys or an external OAuth
            provider. Configure upstream credentials independently. Caller
            tokens are not forwarded to the API, and the builder does not
            resolve secret references.
          </Text>
        </section>
        <section>
          <Title order={2}>Run one reviewed tool branch per call</Title>
          <Text mt="sm">
            Each tool selects a branch that calls an HTTP API or runs native
            processing, then returns an MCP result. You can expose several
            endpoints with separate paths, catalogues and permissions. Hookway
            hosts your configured tools; it is not a proxy that aggregates
            arbitrary existing MCP servers.
          </Text>
        </section>
        <section>
          <Title order={2}>Deliver business events to compatible clients</Title>
          <Text mt="sm">
            MCP Events subscriptions receive matching named events through
            signed callbacks. Clients must support the event extension. Callback
            acceptance confirms receipt, not completion of an agent task.
            Delivery is at least once and unordered, without historical
            catch-up.
          </Text>
          <Anchor component={Link} to={docsHref("docs/guides/mcp-events.md")}>
            Read MCP Events compatibility and delivery boundaries
          </Anchor>
        </section>
        <section>
          <Title order={2}>Review before you connect</Title>
          <Text mt="sm">
            The importer supports selected JSON operations, not every OpenAPI
            feature. Browser connections also need a reachable endpoint and an
            allowed origin. Connect &amp; test runs live tools against your
            deployment, including permitted mutations.
          </Text>
          <Group mt="md">
            <Anchor
              component={Link}
              to={docsHref("docs/guides/mcp-gateway.md")}
            >
              Gateway configuration
            </Anchor>
            <Anchor
              component={Link}
              to={docsHref("docs/reference/security.md")}
            >
              Security model
            </Anchor>
            <Anchor component={Link} to="/compare/">
              Compare workflows
            </Anchor>
          </Group>
        </section>
      </Stack>
      <CtaBand />
    </WebsitePage>
  );
}
export function AboutPage() {
  return (
    <WebsitePage
      title="About Hookway"
      lead="An MIT-licensed, self-hosted gateway for receiving, verifying and routing webhooks, handling HTTP requests, and exposing selected APIs as MCP tools."
    >
      <Stack gap="xl">
        <section>
          <Title order={2}>A project you run in your infrastructure</Title>
          <Text mt="sm">
            Hookway is maintained by Jason Giroux. Core runs as a Go binary or
            container configured in YAML. The public visual builder creates
            configuration without a hosted account. Replay and its operations
            console are optional services you deploy separately.
          </Text>
          <Text mt="sm">
            The MIT license permits use, modification and distribution under its
            terms. You operate and pay for your own infrastructure; this website
            does not sell a hosted event-delivery service.
          </Text>
          <Group mt="md">
            <Anchor href={GITHUB_REPO}>Source and project discussions</Anchor>
            <Anchor href={GITHUB_REPO + "/blob/main/LICENSE"}>
              MIT license
            </Anchor>
          </Group>
        </section>
        <section>
          <Title order={2}>Evidence and operating boundaries</Title>
          <List mt="sm">
            <List.Item>
              <Anchor
                component={Link}
                to={docsHref("docs/evidence/performance.md")}
              >
                Measured workloads and methodology
              </Anchor>
            </List.Item>
            <List.Item>
              <Anchor
                component={Link}
                to={docsHref("docs/operations/delivery-guarantees.md")}
              >
                Acknowledgements and delivery guarantees
              </Anchor>
            </List.Item>
            <List.Item>
              <Anchor
                component={Link}
                to={docsHref("docs/reference/security.md")}
              >
                Security and credential boundaries
              </Anchor>
            </List.Item>
          </List>
          <Text mt="sm">
            Your applications own consumption and business processing. Webhook
            replay capture is best effort, and published benchmark results are
            not production capacity guarantees.
          </Text>
        </section>
        <section>
          <Title order={2}>Contact and contribute</Title>
          <Text mt="sm">
            Use <Anchor href={GITHUB_REPO + "/issues"}>GitHub issues</Anchor>{" "}
            for questions and reproducible bugs. Keep credentials, personal
            information and private payloads out of public reports. Follow the
            repository’s{" "}
            <Anchor href={GITHUB_REPO + "/security/policy"}>
              security policy
            </Anchor>{" "}
            for vulnerabilities.
          </Text>
        </section>
      </Stack>
    </WebsitePage>
  );
}
export function PrivacyPage() {
  const { openPreferences } = useAnalyticsPreferences();
  return (
    <WebsitePage
      title="Website privacy and analytics"
      lead="The public Hookway website and hosted builder offer optional analytics. Your self-hosted Hookway instance is separate and does not send these website events."
    >
      <Stack gap="xl">
        <Text size="sm">Updated October 31, 2026</Text>
        <section>
          <Title order={2}>Your choice comes first</Title>
          <Text mt="sm">
            Optional analytics stay off until you choose Allow. Decline keeps
            them off. We remember your preference in this browser’s local
            storage. You can change it at any time using Analytics preferences
            in the footer. Withdrawing permission stops collection and clears
            the website’s analytics persistence; it does not erase events
            already received.
          </Text>
          <Button onClick={openPreferences} variant="default" mt="md">
            Analytics preferences
          </Button>
        </section>
        <section>
          <Title order={2}>What optional analytics collect</Title>
          <Text mt="sm">
            When enabled, PostHog receives page paths, a coarse page category, a
            referring website hostname, recognized campaign labels, and explicit
            actions such as opening the builder, copying the quick start,
            exporting a configuration, or opening GitHub. An anonymous browser
            identifier groups consented visits. We do not identify visitors by
            account or email.
          </Text>
          <Text mt="sm">
            We strip query strings and URL fragments from event URLs. We do not
            record sessions or automatically capture clicks, forms or
            exceptions. We do not send configurations, webhook bodies, API
            specifications, credentials, custom filenames or editor contents. An
            export action does not establish that a file was saved or Hookway
            was deployed.
          </Text>
        </section>
        <section>
          <Title order={2}>Providers, storage and requests</Title>
          <Text mt="sm">
            The website serves its fonts locally. Vercel serves the website and
            may process network information, including IP addresses, in the
            course of hosting it. If you allow analytics, PostHog processes the
            permitted events and the network information required to receive
            them. The dedicated Hookway project uses PostHog Cloud’s free-plan
            event retention of one year. This limits queryable event history and
            is not a guarantee of deletion of all provider copies at that time.
            See PostHog’s event-retention documentation.
          </Text>
          <Text mt="sm">
            The hosted builder also stores your workspace locally in your
            browser so you can return to it. This is separate from optional
            analytics. Configuration validation and OpenAPI import normally run
            in the browser. If the browser validator is unavailable, the editor
            can try a validation endpoint on the same website origin with the
            submitted configuration. This is separate from PostHog. The
            self-hosted product has its own deployment settings.
          </Text>
          <Text mt="sm">
            For website privacy questions, use{" "}
            <Anchor href={GITHUB_REPO + "/issues"}>
              the project’s GitHub issues
            </Anchor>{" "}
            without posting personal or confidential information. Provider
            information:{" "}
            <Anchor href="https://posthog.com/privacy">PostHog privacy</Anchor>,{" "}
            <Anchor href="https://posthog.com/docs/data/events-retention">
              event retention
            </Anchor>{" "}
            and{" "}
            <Anchor href="https://vercel.com/legal/privacy-policy">
              Vercel privacy
            </Anchor>
            .
          </Text>
        </section>
      </Stack>
    </WebsitePage>
  );
}
export function NotFoundPage() {
  return (
    <WebsitePage
      title="404 — Page not found"
      lead="This page is not available. Choose a starting point below."
    >
      <Group>
        <Button component={Link} to="/">
          Hookway home
        </Button>
        <Button component={Link} to="/docs/" variant="default">
          Browse documentation
        </Button>
      </Group>
    </WebsitePage>
  );
}
