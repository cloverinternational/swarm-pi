#!/usr/bin/env python3
"""
Python Type Owner Resolver for Forge

This script is the Python equivalent of ts_resolve_owners.js.
It parses Python files using the ast module and resolves owner types
for method/attribute accesses using:
1. Type annotations in function signatures
2. Variable annotations
3. Class instantiation sites
4. Import tracking for known types

Usage:
    python3 py_resolve_owners.py <project_root>

Output (JSON to stdout):
    {
        "refs": {
            "src/lib/client.py:42:10": {"name": "send", "owner": "IPCClient", "kind": "method"},
            ...
        },
        "stats": {"files": 28, "refs": 1234, "duration_ms": 450}
    }
"""

import ast
import json
import os
import sys
import time
from pathlib import Path
from typing import Dict, List, Optional, Set, Tuple


def main():
    if len(sys.argv) < 2:
        sys.stderr.write("Usage: py_resolve_owners.py <project_root>\n")
        sys.exit(1)

    project_root = os.path.abspath(sys.argv[1])
    if not os.path.isdir(project_root):
        sys.stderr.write(f"Not a directory: {project_root}\n")
        sys.exit(1)

    start_time = time.time()
    resolver = PythonTypeResolver(project_root)
    resolver.scan_project()

    stats = {
        "files": resolver.file_count,
        "refs": resolver.ref_count,
        "duration_ms": int((time.time() - start_time) * 1000),
    }

    sys.stderr.write(
        f"[Forge PY] Resolved {resolver.ref_count} typed refs "
        f"across {resolver.file_count} files in {stats['duration_ms']}ms\n"
    )

    output = {"refs": resolver.refs, "stats": stats}
    print(json.dumps(output))


class PythonTypeResolver:
    def __init__(self, project_root: str):
        self.project_root = project_root
        self.refs: Dict[str, dict] = {}
        self.ref_count = 0
        self.file_count = 0
        
        # Track defined classes and their methods
        self.classes: Dict[str, Set[str]] = {}  # class_name -> method_names
        
        # Track imports: module -> local_name
        # Used to resolve types from type hints
        self.imports: Dict[str, str] = {}  # local_name -> full_path
        
        # Track variable types in current scope
        # This is per-file, reset for each file
        self.var_types: Dict[str, str] = {}

    def scan_project(self):
        """Walk project root and process all .py files."""
        for root, dirs, files in os.walk(self.project_root):
            # Skip hidden and common non-source directories
            dirs[:] = [d for d in dirs if not d.startswith(".") and d not in (
                "node_modules", "__pycache__", ".venv", "venv", "env",
                "build", "dist", "egg-info", ".git", ".tox", ".mypy_cache"
            )]
            
            for fname in files:
                if fname.endswith(".py") and not fname.startswith("."):
                    fpath = os.path.join(root, fname)
                    self.process_file(fpath)

    def process_file(self, fpath: str):
        """Parse a single Python file and extract typed references."""
        try:
            with open(fpath, "r", encoding="utf-8", errors="replace") as f:
                source = f.read()
        except Exception as e:
            sys.stderr.write(f"[Forge PY] Warning: could not read {fpath}: {e}\n")
            return

        rel_path = os.path.relpath(fpath, self.project_root)
        self.file_count += 1

        try:
            tree = ast.parse(source, filename=fpath)
        except SyntaxError:
            return

        # Reset per-file state
        self.var_types = {}
        self.imports = {}

        # Pass 1: Collect imports and class definitions
        for node in ast.walk(tree):
            self._collect_imports(node)
            self._collect_classes(node)

        # Pass 2: Collect variable types from annotations and assignments
        self._collect_var_types(tree)

        # Pass 3: Extract attribute accesses with resolved owner types
        self._extract_refs(tree, rel_path)

    def _collect_imports(self, node: ast.AST):
        """Collect import statements for type resolution."""
        if isinstance(node, ast.Import):
            for alias in node.names:
                name = alias.asname or alias.name.split(".")[0]
                self.imports[name] = alias.name
        elif isinstance(node, ast.ImportFrom):
            module = node.module or ""
            for alias in node.names:
                name = alias.asname or alias.name
                self.imports[name] = f"{module}.{alias.name}" if module else alias.name

    def _collect_classes(self, node: ast.AST):
        """Collect class definitions and their methods."""
        if isinstance(node, ast.ClassDef):
            methods = set()
            for item in node.body:
                if isinstance(item, ast.FunctionDef):
                    methods.add(item.name)
            self.classes[node.name] = methods

    def _collect_var_types(self, tree: ast.AST):
        """Collect variable types from annotations and assignments."""
        for node in ast.walk(tree):
            # Variable annotations: var: Type
            if isinstance(node, ast.AnnAssign) and node.target:
                if isinstance(node.target, ast.Name) and node.annotation:
                    type_name = self._get_type_name(node.annotation)
                    if type_name:
                        self.var_types[node.target.id] = type_name

            # Function arguments with type annotations
            elif isinstance(node, ast.FunctionDef):
                # self parameter type inference from class context
                if node.args.args and node.args.args[0].arg == "self":
                    # Find enclosing class
                    pass  # Handled in _extract_refs

                # Regular arguments
                for arg in node.args.args:
                    if arg.annotation and arg.arg != "self":
                        type_name = self._get_type_name(arg.annotation)
                        if type_name:
                            self.var_types[arg.arg] = type_name

            # Assignments from constructor calls: var = ClassName()
            elif isinstance(node, ast.Assign):
                if len(node.targets) == 1 and isinstance(node.targets[0], ast.Name):
                    var_name = node.targets[0].id
                    type_name = self._infer_type_from_expr(node.value)
                    if type_name:
                        self.var_types[var_name] = type_name

    def _get_type_name(self, annotation: ast.AST) -> Optional[str]:
        """Extract type name from a type annotation."""
        if isinstance(annotation, ast.Name):
            return annotation.id
        elif isinstance(annotation, ast.Attribute):
            # e.g., typing.List, module.ClassName
            parts = []
            node = annotation
            while isinstance(node, ast.Attribute):
                parts.append(node.attr)
                node = node.value
            if isinstance(node, ast.Name):
                parts.append(node.id)
            return ".".join(reversed(parts))
        elif isinstance(annotation, ast.Subscript):
            # e.g., List[str], Optional[int] - get the base type
            if isinstance(annotation.value, ast.Name):
                return annotation.value.id
            elif isinstance(annotation.value, ast.Attribute):
                return self._get_type_name(annotation.value)
        return None

    def _infer_type_from_expr(self, expr: ast.AST) -> Optional[str]:
        """Infer type from an expression (mainly constructor calls)."""
        if isinstance(expr, ast.Call):
            # ClassName() or module.ClassName()
            return self._get_func_name(expr.func)
        return None

    def _get_func_name(self, func: ast.AST) -> Optional[str]:
        """Get function/class name from a call expression."""
        if isinstance(func, ast.Name):
            return func.id
        elif isinstance(func, ast.Attribute):
            # module.Class or obj.method - we want the class
            if isinstance(func.value, ast.Name):
                return f"{func.value.id}.{func.attr}"
            return func.attr
        return None

    def _extract_refs(self, tree: ast.AST, rel_path: str):
        """Extract attribute accesses with resolved owner types."""
        # Track current class context for self.attr accesses
        current_class: Optional[str] = None

        class ClassVisitor(ast.NodeVisitor):
            def __init__(self, outer):
                self.outer = outer
                self.class_stack: List[str] = []

            def visit_ClassDef(self, node: ast.ClassDef):
                self.class_stack.append(node.name)
                self.generic_visit(node)
                self.class_stack.pop()

            def visit_FunctionDef(self, node: ast.FunctionDef):
                # Visit function body, passing class context
                self.generic_visit(node)

            def visit_Attribute(self, node: ast.Attribute):
                outer = self.outer
                owner_type = None

                # self.attr or cls.attr - owner is current class
                if isinstance(node.value, ast.Name) and node.value.id in ("self", "cls"):
                    if self.class_stack:
                        owner_type = self.class_stack[-1]

                # var.attr - look up var's type
                elif isinstance(node.value, ast.Name):
                    var_name = node.value.id
                    owner_type = outer.var_types.get(var_name)

                # Record if we found an owner type
                if owner_type:
                    # Determine if it's a method or property
                    kind = "property"
                    if owner_type in outer.classes and node.attr in outer.classes[owner_type]:
                        kind = "method"

                    # The attribute name appears after the dot.
                    # node.col_offset is the start of the whole expression (e.g. "result").
                    # We need the column of the attribute name itself (e.g. "returncode").
                    # Use end_col_offset if available (Python 3.8+), else estimate.
                    attr_col = node.col_offset + 1
                    if hasattr(node, "end_col_offset"):
                        # end_col_offset points to the end of the attribute name
                        # attribute name starts at end_col_offset - len(node.attr)
                        attr_col = node.end_col_offset - len(node.attr) + 1
                    else:
                        # Fallback: assume "value.attr" form, so attr is at col_offset + 1 + len(value_name) + 1
                        if isinstance(node.value, ast.Name):
                            attr_col = node.col_offset + 1 + len(node.value.id) + 1

                    key = f"{rel_path}:{node.lineno}:{attr_col}"
                    outer.refs[key] = {
                        "name": node.attr,
                        "owner": owner_type,
                        "kind": kind,
                    }
                    outer.ref_count += 1

                self.generic_visit(node)

        visitor = ClassVisitor(self)
        visitor.visit(tree)


if __name__ == "__main__":
    main()
