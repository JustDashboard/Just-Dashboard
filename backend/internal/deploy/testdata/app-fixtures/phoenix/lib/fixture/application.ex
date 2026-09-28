defmodule Fixture.Application do
  use Application

  @impl true
  def start(_type, _args) do
    children = [FixtureWeb.Endpoint]
    Supervisor.start_link(children, strategy: :one_for_one, name: Fixture.Supervisor)
  end
end
