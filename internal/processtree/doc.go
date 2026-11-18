// Package processtree is the launch boundary for processes whose descendants
// must be ended together. Windows starts suspended and joins a job before
// execution; Unix sets the process group before exec.
package processtree
