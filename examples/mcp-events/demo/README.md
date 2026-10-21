# Issue events and agent tools demo

A disposable Compose stack includes a test-only OAuth/OIDC provider, PostgreSQL,
read-only issue API, core, replay and console. It does not use a personal identity
provider or GitHub credentials. The provider deliberately issues synthetic test
identities; never connect this configuration to production data.

From the repository root:

```sh
python3 examples/mcp-events/demo/setup.py
DEMO_SERVER_SHA=$(git rev-parse HEAD) docker compose -f examples/mcp-events/demo/docker-compose.yaml up --build -d
python3 examples/mcp-events/demo/smoke.py
```

`setup.py` generates a private `.env`, retaining existing credentials/encryption
keys on subsequent runs. It also produces ignored runtime IdP configuration and
`plugin.zip`. No credential is printed or included in the plugin package.
The smoke check performs authorization code with S256 PKCE, validates discovery,
calls `get_issue`, and checks wrong-audience rejection. It proves OAuth and tools,
not an agent wake-up. Automated signed callback/recovery tests run separately via
`make event-integration-test`.

Wait for the services to finish starting before running the smoke check. After
editing a fixture script, recreate its service so the Python process loads the
new source. Use a clean committed tree for hosted acceptance; `DEMO_SERVER_SHA`
records that revision in the service build information.

The console is available at [localhost:18082](http://localhost:18082). Read the
operator token from the local `.env` when signing in. Operator, control and
webhook secrets are separate. Use **MCP events** to inspect subscriptions,
delivery attempts and administrative recovery.

The source configurations are [core.yaml](core.yaml) and [replay.yaml](replay.yaml).
All services have private Docker-network addresses; published ports bind only to
loopback. `fixture.py` serves deterministic issue details. `get_issue` uses normal
Hookway HTTP request and MCP result nodes. `lookup_evidence` is available only in
the lookup result, not in the event summary.

## Hosted acceptance

1. Start the optional temporary tunnel:

   ```sh
   docker compose -f examples/mcp-events/demo/docker-compose.yaml --profile live up -d tunnel
   docker compose -f examples/mcp-events/demo/docker-compose.yaml logs tunnel
   ```

2. Pass its HTTPS origin to setup, then recreate the affected services:

   ```sh
   python3 examples/mcp-events/demo/setup.py --public-url <https-origin>
   docker compose -f examples/mcp-events/demo/docker-compose.yaml up -d --force-recreate idp core front
   ```

   The tunnel front permits only MCP, protected-resource discovery, demo OAuth
   endpoints and optional portal domain verification. It does not expose the
   replay operator API, private control listener, metrics, fixture API or webhook
   ingress. Domain verification, if required by the portal, uses a local
   `DEMO_DOMAIN_CHALLENGE` value followed by recreating `front`.

3. In [ChatGPT Plugins](https://chatgpt.com/plugins), select **Add → Add custom
   MCP server**. Enter the tunnel URL followed by `/agents/mcp`, choose OAuth,
   and open **Advanced OAuth settings**. Use **User-Defined OAuth Client**, client
   ID `demo-client`, token endpoint authentication `none`, and base scopes
   `openid`, `issues:read`, and `events:read` (one per line). Leave the secret empty.
   Review the test connection and create the personal plugin, then connect it.
   The synthetic IdP supplies the test identity automatically. This personal cloud
   connection requires no submission, review, domain challenge, or publication.
   Refresh tools/events after changing the server. See the official
   [custom MCP connection instructions](https://developers.openai.com/plugins/deploy/connect-chatgpt).

   `setup.py` also prepares an optional private `plugin.zip` for package testing;
   the direct connection above does not need it. Keep it outside the public
   submission portal. It contains a public client ID and no credential.
4. Ask the agent: “Monitor issue.opened for repository acme/app. Whenever an issue
   arrives, call get_issue with its issue_id and report lookup_evidence.” Confirm
   discovery, subscribe and callback challenge verification in the console.
5. Send a fresh numeric issue ID and confirm an agent turn and `get_issue` call:

   ```sh
   python3 examples/mcp-events/demo/send.py --event-id 42001
   python3 examples/mcp-events/demo/send.py --event-id 43001 --nonmatching
   ```

6. Confirm the second event is excluded. Stop monitoring in ChatGPT, verify
   unsubscribe, and send another fresh matching ID to confirm delivery stops.
7. Record client/protocol, exact server SHA, subscription/event IDs, sanitized
   callback evidence, and the observed `get_issue` result. Never record tokens,
   callback paths/query strings or signatures. A callback `2xx` alone is not proof
   of hosted-agent execution. Re-run the smoke test against public endpoints by
   setting `DEMO_BASE_URL` to the tunnel origin and `DEMO_IDP_URL` to its `/demo` URL.

## Cleanup

```sh
docker compose -f examples/mcp-events/demo/docker-compose.yaml --profile live down
```

This preserves the database and private credentials for another run. Only add
`--volumes` when deliberately deleting the disposable demo data. Keep `.env` and
its encryption key whenever reusing the database.
