# syntax=docker/dockerfile:1

# BRouter (route engine), built from upstream source. Upstream publishes no image
# past v1.7.8, whose lookups.dat is v10, while brouter.de segments are lookup v11
# (BRouter 1.7.9+). Bump procedure: change BROUTER_REF only.

# ---- stage 1: build the server fat jar (bytecode is arch-neutral) ----------
FROM --platform=$BUILDPLATFORM eclipse-temurin:17-jdk AS build
ARG BROUTER_REF=4d2639af77ea5ed9c30d3e400764eb6f9e8522da
ADD --keep-git-dir=false https://github.com/abrensch/brouter.git#${BROUTER_REF} /src
WORKDIR /src
RUN sed -i 's/\r$//' gradlew && chmod +x gradlew \
 && ./gradlew --no-daemon :brouter-server:fatJar -x test \
 && cp brouter-server/build/libs/brouter-*-all.jar /brouter.jar

# ---- stage 2: runtime -------------------------------------------------------
# Pinned to the eclipse-temurin:17-jre tag as of 2026-11-03; bump by
# re-pulling the tag and updating both the digest and this date.
FROM eclipse-temurin:17-jre@sha256:2a996a534890f1bee252fab23a0c01e773e9e1dc0e4700e8f0f6a2697c6cbf76
ARG BROUTER_REF=4d2639af77ea5ed9c30d3e400764eb6f9e8522da
LABEL org.opencontainers.image.title="wattson-brouter" \
      org.opencontainers.image.source="https://github.com/metril/wattson" \
      org.opencontainers.image.description="BRouter route engine for Wattson" \
      org.opencontainers.image.base.name="eclipse-temurin:17-jre" \
      io.github.metril.brouter.ref="${BROUTER_REF}"
COPY --from=build /brouter.jar /app/brouter.jar
COPY --from=build /src/misc/profiles2/ /profiles2/
RUN mkdir -p /segments4 /customprofiles && chown 65532:65532 /segments4 /customprofiles
USER 65532:65532
EXPOSE 17777
# RouteServer args: segments profiles customprofiles port maxthreads bindaddress
ENTRYPOINT ["java", "-Xmx128M", "-Xms128M", "-Xmn8M", "-DmaxRunningTime=300", "-DuseRFCMimeType=false", \
  "-cp", "/app/brouter.jar", "btools.server.RouteServer", \
  "/segments4", "/profiles2", "/customprofiles", "17777", "1", "0.0.0.0"]
