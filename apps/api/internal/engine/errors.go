package engine

type NodeExecutionError struct {
	NodeID   string
	NodeType string
	Err      error
}

func (err *NodeExecutionError) Error() string {
	return "node execution failed"
}

func (err *NodeExecutionError) Unwrap() error {
	return err.Err
}
