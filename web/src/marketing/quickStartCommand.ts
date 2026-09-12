import { GITHUB_REPO } from "./links";

// The one command that gets a reader from nothing to a running stack. Built
// from GITHUB_REPO so it cannot drift from where the code actually lives;
// `make dev-up` brings up the example docker-compose topology.
export const QUICK_START_COMMAND = `git clone ${GITHUB_REPO}.git && cd hook_way && make dev-up`;
