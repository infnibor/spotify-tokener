# Base Playwright image
FROM mcr.microsoft.com/playwright:v1.56.1-jammy

# Install only the bare minimum for Bun
RUN apt-get update \
    && apt-get install -y --no-install-recommends unzip curl \
    && rm -rf /var/lib/apt/lists/*

# Install Bun 
RUN curl -fsSL https://bun.sh/install | bash
ENV PATH="/root/.bun/bin:${PATH}"

WORKDIR /app

# Copy only package files first 
COPY package.json bun.lockb ./

# Install dependencies
RUN bun install --production

# Copy the rest of the app
COPY . .

EXPOSE 8080

# Run using bun
CMD ["bun", "run", "src/app.ts"]
