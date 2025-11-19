FROM mcr.microsoft.com/playwright:v1.56.1-jammy

# Install unzip (required for bun installer)
RUN apt-get update && apt-get install -y unzip curl && rm -rf /var/lib/apt/lists/*

# Install Bun
RUN curl -fsSL https://bun.sh/install | bash
ENV PATH="/root/.bun/bin:${PATH}"

WORKDIR /app

COPY package.json ./

# Install dependencies
RUN bun install

COPY . .

EXPOSE 8080

CMD ["bun", "run", "src/app.ts"]
