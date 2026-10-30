import type { DeploymentRunDiagnosis } from "../../src/lib/types"

/**
 * What `TestLiveFailedBuildIsDiagnosedByARealModel` recorded: a Node build
 * whose script needs a package it never declared, built through the real
 * builder and BuildKit, the cause the dashboard's own rules named, and what a
 * real model on this machine (qwen2.5 3B under Ollama) answered about it,
 * read by the server's answer reader. The run page's pictures replay it.
 * Regenerate it from that test's `JD_TEST_DIAGNOSIS_OUT` file.
 */

export const liveDiagnosis: DeploymentRunDiagnosis = {
  runId: 84,
  provider: "openai",
  model: "jd-diagnosis-qwen2.5-3b",
  answer: {
    cause:
      "Code analysis of the transcript revealed that a required module 'left-pad' was missing in the node_modules directory. This typically occurs due to missing or incorrect installation instructions within the build process.",
    evidence: [
      {
        seq: 43,
        text: "#10 0.923 Error: Cannot find module 'left-pad'",
      },
      {
        seq: 46,
        text: "#10 0.923     at Function._resolveFilename (node:internal/modules/cjs/loader:1430:15)",
      },
      {
        seq: 47,
        text: "#10 0.923     at defaultResolveImpl (node:internal/modules/cjs/loader:1040:19)",
      },
      {
        seq: 48,
        text: "#10 0.923     at resolveForCJSWithHooks (node:internal/modules/cjs/loader:1045:22)",
      },
    ],
    steps: [
      "Review the build command (`npm run build`), confirming it is correctly defined in the `package.json`.",
      "Examine the project's package.json for any conditional dependencies or installation scripts.",
      "Check whether 'left-pad' was installed correctly by using the npm audit to see if it has any known vulnerabilities that could have prevented its installation.",
    ],
    text: "CAUSE: Code analysis of the transcript revealed that a required module 'left-pad' was missing in the node_modules directory. This typically occurs due to missing or incorrect installation instructions within the build process.\n\nEVIDENCE:\n> 43 | #10 0.923 Error: Cannot find module 'left-pad'\n> 47 | #10 0.923     at Function._resolveFilename (node:internal/modules/cjs/loader:1430:15)\n> 48 | #10 0.923     at defaultResolveImpl (node:internal/modules/cjs/loader:1040:19)\n> 49 | #10 0.923     at resolveForCJSWithHooks (node:internal/modules/cjs/loader:1045:22)\n\nSTEPS:\n1. Review the build command (`npm run build`), confirming it is correctly defined in the `package.json`.\n2. Examine the project's package.json for any conditional dependencies or installation scripts.\n3. Check whether 'left-pad' was installed correctly by using the npm audit to see if it has any known vulnerabilities that could have prevented its installation.\n\nNext, look for any logs or messages preceding #9 where 'npm install --no-audit --no-fund' is run, as there might be a failed module resolution error there which can also lead to an `npm install` failure. Additionally, verify if the left-pad package name and path are case-sensitive by searching the codebase for mentions of 'leftpad'.",
  },
  sent: "A deployment run failed. What the dashboard recorded about it follows. Values it masked read [REDACTED].\n\nRun\n- Operation: deploy, started by a push to the repository\n- Outcome: failed after 1m30s\n- Code: build_module_not_found\n- Reason: The build command (`npm run build`) exited with code 1: the build imports left-pad, which is not installed or does not exist. Add it to the project's dependencies, or correct the import path; paths on Linux are case-sensitive.\n\nFailed step: Build (build_artifact), attempt 1\n- Code: build_module_not_found\n- Message: The build command (`npm run build`) exited with code 1: the build imports left-pad, which is not installed or does not exist. Add it to the project's dependencies, or correct the import path; paths on Linux are case-sensitive.\n\nThe dashboard's own reading of the failure, from its rules rather than a model:\n- Cause: code build_module_not_found, phase build, command `npm run build`, exit code 1\n- It names: left-pad\n- The transcript line that shows it: 43\n\nSettings this run used\n- Source: git, branch main, commit 4444444\n- Build: method recipe, recipe node, package manager npm, build command `npm run build`, start command `npm start`\n- Runtime: listens on 3000, strategy blue_green\n- Variables, by name and scope (their values are never sent): NPM_TOKEN (build, secret)\n\nSteps\n1. Fetch source — passed\n2. Check plan — passed\n3. Prepare build context — passed\n4. Build — failed: build_module_not_found\n\nTranscript: 66 lines, each as sequence | text\n== Build ==\n1 | #0 building with \"default\" instance using docker driver\n3 | #1 [internal] load build definition from Dockerfile\n4 | #1 transferring dockerfile: 760B done\n5 | #1 DONE 0.0s\n7 | #2 resolve image config for docker-image://docker.io/docker/dockerfile:1.10\n8 | #2 DONE 0.3s\n10 | #3 docker-image://docker.io/docker/dockerfile:1.10@sha256:865e5dd094beca432e8c0a1d5e1c465db5f998dca4e439981029b3b81fb39ed5\n11 | #3 resolve docker.io/docker/dockerfile:1.10@sha256:865e5dd094beca432e8c0a1d5e1c465db5f998dca4e439981029b3b81fb39ed5 0.0s done\n12 | #3 CACHED\n14 | #4 [internal] load metadata for docker.io/library/node:22-alpine@sha256:fe4f6d74e20370acb04ad889077d14d0f64a9c8ea6a42bc60cddf1e844699914\n15 | #4 DONE 0.6s\n17 | #5 [build 1/5] FROM docker.io/library/node:22-alpine@sha256:fe4f6d74e20370acb04ad889077d14d0f64a9c8ea6a42bc60cddf1e844699914\n18 | #5 resolve docker.io/library/node:22-alpine@sha256:fe4f6d74e20370acb04ad889077d14d0f64a9c8ea6a42bc60cddf1e844699914 0.0s done\n19 | #5 DONE 0.0s\n21 | #6 [internal] load build context\n22 | #6 transferring context: 276B done\n23 | #6 DONE 0.0s\n25 | #7 [build 2/5] WORKDIR /app\n26 | #7 CACHED\n28 | #8 [build 3/5] COPY . .\n29 | #8 CACHED\n31 | #9 [build 4/5] RUN npm install --no-audit --no-fund\n32 | #9 CACHED\n34 | #10 [build 5/5] RUN npm run build\n35 | #10 0.859 \n36 | #10 0.859 > diagnosis-fixture@1.0.0 build\n37 | #10 0.859 > node build.js\n38 | #10 0.859 \n39 | #10 0.923 node:internal/modules/cjs/loader:1433\n40 | #10 0.923   throw err;\n41 | #10 0.923   ^\n42 | #10 0.923 \n43 | #10 0.923 Error: Cannot find module 'left-pad'\n44 | #10 0.923 Require stack:\n45 | #10 0.923 - /app/build.js\n46 | #10 0.923     at Function._resolveFilename (node:internal/modules/cjs/loader:1430:15)\n47 | #10 0.923     at defaultResolveImpl (node:internal/modules/cjs/loader:1040:19)\n48 | #10 0.923     at resolveForCJSWithHooks (node:internal/modules/cjs/loader:1045:22)\n49 | #10 0.923     at Function._load (node:internal/modules/cjs/loader:1216:25)\n50 | #10 0.923     at wrapModuleLoad (node:internal/modules/cjs/loader:254:19)\n51 | #10 0.923     at Module.require (node:internal/modules/cjs/loader:1527:12)\n52 | #10 0.923     at require (node:internal/modules/helpers:147:16)\n53 | #10 0.923     at Object.<anonymous> (/app/build.js:1:13)\n54 | #10 0.923     at Module._compile (node:internal/modules/cjs/loader:1781:14)\n55 | #10 0.923     at Object..js (node:internal/modules/cjs/loader:1913:10) {\n56 | #10 0.923   code: 'MODULE_NOT_FOUND',\n57 | #10 0.923   requireStack: [ '/app/build.js' ]\n58 | #10 0.923 }\n59 | #10 0.923 \n60 | #10 0.923 Node.js v22.23.4\n61 | #10 ERROR: process \"/bin/sh -c npm run build\" did not complete successfully: exit code: 1\n62 | ------\n63 |  > [build 5/5] RUN npm run build:\n64 | 0.923     at Module.require (node:internal/modules/cjs/loader:1527:12)\n65 | 0.923     at require (node:internal/modules/helpers:147:16)\n66 | 0.923     at Object.<anonymous> (/app/build.js:1:13)\n67 | 0.923     at Module._compile (node:internal/modules/cjs/loader:1781:14)\n68 | 0.923     at Object..js (node:internal/modules/cjs/loader:1913:10) {\n69 | 0.923   code: 'MODULE_NOT_FOUND',\n70 | 0.923   requireStack: [ '/app/build.js' ]\n71 | 0.923 }\n72 | 0.923 \n73 | 0.923 Node.js v22.23.4\n74 | ------\n75 | ERROR: failed to build: failed to solve: process \"/bin/sh -c npm run build\" did not complete successfully: exit code: 1\n76 | Diagnosis: The build command (`npm run build`) exited with code 1: the build imports left-pad, which is not installed or does not exist. Add it to the project's dependencies, or correct the import path; paths on Linux are case-sensitive.\n",
  sentLines: 66,
  masked: 0,
  createdBy: "operator",
  createdAt: "2026-10-26T01:30:37.068750826Z",
  modelLabel: "jd-diagnosis-qwen2.5-3b",
}

export const liveCause: Record<string, unknown> = {
  code: "build_module_not_found",
  phase: "build",
  command: "npm run build",
  exitCode: 1,
  subjects: ["left-pad"],
  lineSeq: 43,
}

export const liveTranscript: { seq: number; text: string }[] = [
  {
    seq: 1,
    text: '#0 building with "default" instance using docker driver',
  },
  {
    seq: 3,
    text: "#1 [internal] load build definition from Dockerfile",
  },
  {
    seq: 4,
    text: "#1 transferring dockerfile: 760B done",
  },
  {
    seq: 5,
    text: "#1 DONE 0.0s",
  },
  {
    seq: 7,
    text: "#2 resolve image config for docker-image://docker.io/docker/dockerfile:1.10",
  },
  {
    seq: 8,
    text: "#2 DONE 0.3s",
  },
  {
    seq: 10,
    text: "#3 docker-image://docker.io/docker/dockerfile:1.10@sha256:865e5dd094beca432e8c0a1d5e1c465db5f998dca4e439981029b3b81fb39ed5",
  },
  {
    seq: 11,
    text: "#3 resolve docker.io/docker/dockerfile:1.10@sha256:865e5dd094beca432e8c0a1d5e1c465db5f998dca4e439981029b3b81fb39ed5 0.0s done",
  },
  {
    seq: 12,
    text: "#3 CACHED",
  },
  {
    seq: 14,
    text: "#4 [internal] load metadata for docker.io/library/node:22-alpine@sha256:fe4f6d74e20370acb04ad889077d14d0f64a9c8ea6a42bc60cddf1e844699914",
  },
  {
    seq: 15,
    text: "#4 DONE 0.6s",
  },
  {
    seq: 17,
    text: "#5 [build 1/5] FROM docker.io/library/node:22-alpine@sha256:fe4f6d74e20370acb04ad889077d14d0f64a9c8ea6a42bc60cddf1e844699914",
  },
  {
    seq: 18,
    text: "#5 resolve docker.io/library/node:22-alpine@sha256:fe4f6d74e20370acb04ad889077d14d0f64a9c8ea6a42bc60cddf1e844699914 0.0s done",
  },
  {
    seq: 19,
    text: "#5 DONE 0.0s",
  },
  {
    seq: 21,
    text: "#6 [internal] load build context",
  },
  {
    seq: 22,
    text: "#6 transferring context: 276B done",
  },
  {
    seq: 23,
    text: "#6 DONE 0.0s",
  },
  {
    seq: 25,
    text: "#7 [build 2/5] WORKDIR /app",
  },
  {
    seq: 26,
    text: "#7 CACHED",
  },
  {
    seq: 28,
    text: "#8 [build 3/5] COPY . .",
  },
  {
    seq: 29,
    text: "#8 CACHED",
  },
  {
    seq: 31,
    text: "#9 [build 4/5] RUN npm install --no-audit --no-fund",
  },
  {
    seq: 32,
    text: "#9 CACHED",
  },
  {
    seq: 34,
    text: "#10 [build 5/5] RUN npm run build",
  },
  {
    seq: 35,
    text: "#10 0.859 ",
  },
  {
    seq: 36,
    text: "#10 0.859 > diagnosis-fixture@1.0.0 build",
  },
  {
    seq: 37,
    text: "#10 0.859 > node build.js",
  },
  {
    seq: 38,
    text: "#10 0.859 ",
  },
  {
    seq: 39,
    text: "#10 0.923 node:internal/modules/cjs/loader:1433",
  },
  {
    seq: 40,
    text: "#10 0.923   throw err;",
  },
  {
    seq: 41,
    text: "#10 0.923   ^",
  },
  {
    seq: 42,
    text: "#10 0.923 ",
  },
  {
    seq: 43,
    text: "#10 0.923 Error: Cannot find module 'left-pad'",
  },
  {
    seq: 44,
    text: "#10 0.923 Require stack:",
  },
  {
    seq: 45,
    text: "#10 0.923 - /app/build.js",
  },
  {
    seq: 46,
    text: "#10 0.923     at Function._resolveFilename (node:internal/modules/cjs/loader:1430:15)",
  },
  {
    seq: 47,
    text: "#10 0.923     at defaultResolveImpl (node:internal/modules/cjs/loader:1040:19)",
  },
  {
    seq: 48,
    text: "#10 0.923     at resolveForCJSWithHooks (node:internal/modules/cjs/loader:1045:22)",
  },
  {
    seq: 49,
    text: "#10 0.923     at Function._load (node:internal/modules/cjs/loader:1216:25)",
  },
  {
    seq: 50,
    text: "#10 0.923     at wrapModuleLoad (node:internal/modules/cjs/loader:254:19)",
  },
  {
    seq: 51,
    text: "#10 0.923     at Module.require (node:internal/modules/cjs/loader:1527:12)",
  },
  {
    seq: 52,
    text: "#10 0.923     at require (node:internal/modules/helpers:147:16)",
  },
  {
    seq: 53,
    text: "#10 0.923     at Object.<anonymous> (/app/build.js:1:13)",
  },
  {
    seq: 54,
    text: "#10 0.923     at Module._compile (node:internal/modules/cjs/loader:1781:14)",
  },
  {
    seq: 55,
    text: "#10 0.923     at Object..js (node:internal/modules/cjs/loader:1913:10) {",
  },
  {
    seq: 56,
    text: "#10 0.923   code: 'MODULE_NOT_FOUND',",
  },
  {
    seq: 57,
    text: "#10 0.923   requireStack: [ '/app/build.js' ]",
  },
  {
    seq: 58,
    text: "#10 0.923 }",
  },
  {
    seq: 59,
    text: "#10 0.923 ",
  },
  {
    seq: 60,
    text: "#10 0.923 Node.js v22.23.4",
  },
  {
    seq: 61,
    text: '#10 ERROR: process "/bin/sh -c npm run build" did not complete successfully: exit code: 1',
  },
  {
    seq: 62,
    text: "------",
  },
  {
    seq: 63,
    text: " > [build 5/5] RUN npm run build:",
  },
  {
    seq: 64,
    text: "0.923     at Module.require (node:internal/modules/cjs/loader:1527:12)",
  },
  {
    seq: 65,
    text: "0.923     at require (node:internal/modules/helpers:147:16)",
  },
  {
    seq: 66,
    text: "0.923     at Object.<anonymous> (/app/build.js:1:13)",
  },
  {
    seq: 67,
    text: "0.923     at Module._compile (node:internal/modules/cjs/loader:1781:14)",
  },
  {
    seq: 68,
    text: "0.923     at Object..js (node:internal/modules/cjs/loader:1913:10) {",
  },
  {
    seq: 69,
    text: "0.923   code: 'MODULE_NOT_FOUND',",
  },
  {
    seq: 70,
    text: "0.923   requireStack: [ '/app/build.js' ]",
  },
  {
    seq: 71,
    text: "0.923 }",
  },
  {
    seq: 72,
    text: "0.923 ",
  },
  {
    seq: 73,
    text: "0.923 Node.js v22.23.4",
  },
  {
    seq: 74,
    text: "------",
  },
  {
    seq: 75,
    text: 'ERROR: failed to build: failed to solve: process "/bin/sh -c npm run build" did not complete successfully: exit code: 1',
  },
  {
    seq: 76,
    text: "Diagnosis: The build command (`npm run build`) exited with code 1: the build imports left-pad, which is not installed or does not exist. Add it to the project's dependencies, or correct the import path; paths on Linux are case-sensitive.",
  },
]
