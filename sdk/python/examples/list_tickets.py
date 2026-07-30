"""List one bounded page without printing credentials or ticket content."""

from __future__ import annotations

import os

from chronodesk import Audience, ChronoDeskClient, ClientCredentials


def main() -> None:
    project_key = required_environment("CHRONODESK_PROJECT_KEY")
    anonymous = ChronoDeskClient(
        required_environment("CHRONODESK_URL"),
        project_key,
    )
    token = anonymous.exchange_client_credentials(
        ClientCredentials(
            client_id=required_environment("CHRONODESK_CLIENT_ID"),
            client_secret=required_environment("CHRONODESK_CLIENT_SECRET"),
            audience=Audience.API,
            scopes=("tickets:read",),
        )
    )
    result = anonymous.with_access_token(token.access_token).list_tickets(limit=20)
    print(
        f"project={project_key} tickets={len(result['data'])} "
        f"request_id={result['meta']['request_id']}"
    )


def required_environment(name: str) -> str:
    value = os.environ.get(name, "")
    if not value:
        raise RuntimeError(f"required environment variable {name} is not set")
    return value


if __name__ == "__main__":
    main()
