# Wishlist

## Routing
- This is a net-new feature but allows basic routing from endpoint after auth to specific destinations based on simple logic based on the content of the webhook's body.
    - route based on header or body

## Config
- (last) Allowing database connector in place of a yaml file to allow for live editing/ changes
    - Allowing import of config.yaml file similar to source control. wipe the existing config and import the config.yaml in it's place
- (last) Allowing supplementary database access (Postgres) to enable replaying payloads
    - will require a cron to clean up messages TTL
