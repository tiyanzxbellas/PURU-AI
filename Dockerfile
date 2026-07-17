FROM node:22-alpine AS build

WORKDIR /app

COPY package.json package-lock.json ./
RUN npm ci

COPY tsconfig.json config.js config.d.ts ./
COPY src/ ./src/

RUN npx tsc

FROM node:22-alpine

WORKDIR /app

COPY package.json package-lock.json ./
RUN npm ci --omit=dev

COPY --from=build /app/dist/ ./dist/
COPY config.js config.d.ts ./

EXPOSE 3000

CMD ["node", "dist/index.js"]
