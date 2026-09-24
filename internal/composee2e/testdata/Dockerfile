# Images for the compose end-to-end test, built from binaries the test
# compiles on the host (a Docker build that downloads modules is not always
# possible locally). The core and replay stages copy the release Dockerfile's
# runtime: same user, binary path, healthcheck, entrypoint, and command.
# TestE2EImagesMatchTheReleaseImages fails if the two drift apart.
FROM scratch AS replay
USER 65532:65532
COPY hookway-replay /usr/local/bin/hookway-replay
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/usr/local/bin/hookway-replay", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/hookway-replay"]
CMD ["serve"]

FROM scratch AS core
USER 65532:65532
COPY hookway /usr/local/bin/hookway
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/usr/local/bin/hookway", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/hookway"]

FROM scratch AS sink
USER 65532:65532
COPY sink /usr/local/bin/sink
ENTRYPOINT ["/usr/local/bin/sink"]
