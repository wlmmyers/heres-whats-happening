# Build stage: install all deps (incl. dev) and compile TS -> dist/
FROM public.ecr.aws/lambda/nodejs:24 AS build
WORKDIR /build
RUN corepack enable
COPY package.json pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY tsconfig.json ./
COPY src ./src
RUN pnpm build

# Runtime stage: flat (hoisted) production node_modules + compiled output.
# pnpm defaults to a symlinked node_modules which the Lambda runtime cannot
# resolve, so install prod deps with the hoisted linker directly into the task root.
FROM public.ecr.aws/lambda/nodejs:24
WORKDIR ${LAMBDA_TASK_ROOT}
RUN corepack enable
COPY package.json pnpm-lock.yaml ./
RUN pnpm install --prod --frozen-lockfile --config.node-linker=hoisted
COPY --from=build /build/dist ./
# dist/handler.js exports `handler`
CMD ["handler.handler"]
