package nodes

import "context"

func ExampleDockerInstanceFromContext() {
	// fn is the Job function that will be executed by the pool.
	fn := func(ctx context.Context) error {
		n, ok := DockerInstanceFromContext(ctx)
		if !ok {
			// Do something with the fact that no node was stored in the context.
		}

		// Now we can use n to operate on the Docker engine.
		n.Version()

		return nil
	}

	// Ignore this, needed for compilation
	fn(context.Background())
}
