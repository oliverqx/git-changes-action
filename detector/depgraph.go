package detector

import (
	"fmt"
  "go/parser"
  "go/token"
	"github.com/ethereum/go-ethereum/common"
	"github.com/kendru/darwin/go/depgraph"
	"github.com/vishalkuo/bimap"
	"golang.org/x/mod/modfile"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// getDependencyGraph returns a dependency graph of all the modules in the go.work file that refer to other modules in the go.work file
// returns a map of module (./my_module)->(./my_module_dependency1,./my_module_dependency2).
// nolint: cyclop
func getDependencyGraph(repoPath string) (moduleDeps map[string][]string, err error) {
	moduleDeps = make(map[string][]string)
  packageDeps := make(map[string][]string)
	// parse the go.work file
	goWorkPath := path.Join(repoPath, "go.work")

	if !common.FileExist(goWorkPath) {
		return nil, fmt.Errorf("go.work file not found in %s", repoPath)
	}

	//nolint: gosec
	workFile, err := os.ReadFile(goWorkPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read go.work file: %w", err)
	}

	parsedWorkFile, err := modfile.ParseWork(goWorkPath, workFile, nil)
	if err != nil {
		return  nil, fmt.Errorf("failed to parse go.work file: %w", err)
	}

	// map of module->dependencies + replaces
	var dependencies map[string][]string
	// bidirectional map of module->module name
	var moduleNames *bimap.BiMap[string, string]
	var packageNames *bimap.BiMap[string, string]
  var packagesPerModule map[string][]string 
  var modulePackageDependencies map[string][]string

	// iterate through each module in the go.work file
	// create a list of dependencies for each module
	// and module names
  // Dependencies = {moduleName: [dependency1, dependency2]}
  // moduleNames = Bi.map{moduleName: moduleRelativePath}
  dependencies, moduleNames, modulePackageDependencies, packageNames, packagesPerModule, err = makeDepMaps(repoPath, parsedWorkFile.Use, IncludedDependenciesConfig{includePackages: true, includeModules: true})

	if err != nil {
		return nil, fmt.Errorf("failed to create dependency maps: %w", err)
	}

	depGraph := depgraph.New()
  packageDepGraph := depgraph.New()
	// build the dependency graph
	for _, module := range parsedWorkFile.Use {
    // This is an array of dependencies, both require and replace dependencies. 
    // That means that all 'replace' are found twice because they're usually also a require
		moduleDeps := dependencies[module.Path]
		for _, dep := range moduleDeps {
			// check if the full package name (e.g. github.com/myorg/myrepo/mymodule) is in the list of modules. If it is, add it as a dependency after renaming
      // This is where dependencies are filtered
      // dep will be fount in moduleNames if its included in the workfile
			renamedDep, hasDep := moduleNames.GetInverse(dep)

			if hasDep {
				err = depGraph.DependOn(module.Path, renamedDep)
				if err != nil {
					return nil, fmt.Errorf("failed to add dependency %s -> %s: %w", module.Path, dep, err)
				}
			}

			if isRelativeDep(dep) {
				// if the dependency is relative, add it as a dependency
				err = depGraph.DependOn(module.Path, dep)
				if err != nil {
					return nil, fmt.Errorf("failed to add dependency %s -> %s: %w", module.Path, dep, err)
				}
			}
		}
	}

	for _, module := range parsedWorkFile.Use {
		for dep := range depGraph.Dependencies(module.Path) {
			moduleDeps[module.Path] = append(moduleDeps[module.Path], dep)
		}
	}

  for _, module := range parsedWorkFile.Use {
     packagesInModule := packagesPerModule[module.Path] 
     for _, modPackage := range packagesInModule {
       renamedPackage, hasPackage := packageNames.Get(modPackage)
       
       if hasPackage {
         for _, dep := range modulePackageDependencies[renamedPackage] {
           dep = strings.TrimPrefix(dep,`"`)
           dep = strings.TrimSuffix(dep,`"`)
           renamedDep, hasDep := packageNames.GetInverse(dep)

           if hasDep {
             err = packageDepGraph.DependOn(modPackage, renamedDep)
           }
         }
       }

       for dep := range packageDepGraph.Dependencies(modPackage) {
         packageDeps[modPackage] = append(packageDeps[modPackage], dep)
        }
    }
  }
	return packageDeps, nil
}

type IncludedDependenciesConfig struct {
  includeModules bool
  includePackages bool
}

func extractGoFiles(pwd string, currentModule string, currentPackage string, goFiles map[string][]string) {
  // We call ls on the given directory
  ls, err := os.ReadDir(pwd)
  if err != nil {
  }

  for _, entry := range ls {
    if entry.IsDir() {
      extractGoFiles(pwd + "/" + entry.Name(), currentModule, entry.Name(), goFiles)
    } else if strings.Contains(entry.Name(), ".go") {
      fileName := pwd + "/" + entry.Name()
      goFiles["/" + currentPackage] = append(goFiles["/" + currentModule + "/" + currentPackage], fileName)
    }

  }
}

// makeDepMaps makes a dependency map and a bidirectional map of dep<->module.
func makeDepMaps(repoPath string, uses []*modfile.Use, config IncludedDependenciesConfig) (dependencies map[string][]string, dependencyNames *bimap.BiMap[string, string], packageDependencies map[string][]string, packageNames *bimap.BiMap[string,string], packagesPerModule map[string][]string, err error) {
	// map of module->dependencies + replaces
	dependencies = make(map[string][]string)
	// bidirectional map of module->module name
	dependencyNames = bimap.NewBiMap[string, string]()

	// iterate through each module in the go.work file
	// create a list of dependencies for each module
	// and module names
  packageNames = bimap.NewBiMap[string,string]()
  modulePackageDependencies := make(map[string][]string)
  packagesPerModule = make(map[string][]string)

	for _, module := range uses {
      modContents, err := os.ReadFile(filepath.Join(repoPath, module.Path, "go.mod"))
      if err != nil {
        return dependencies, dependencyNames, modulePackageDependencies, packageNames, packagesPerModule, fmt.Errorf("failed to read module file %s: %w", module.Path, err)
      }

      parsedModFile, err := modfile.Parse(module.Path, modContents, nil)
      if err != nil {
        return dependencies, dependencyNames, modulePackageDependencies, packageNames, packagesPerModule, fmt.Errorf("failed to parse module file %s: %w", module.Path, err)
      }

    if config.includeModules {
      dependencyNames.Insert(module.Path, parsedModFile.Module.Mod.Path)
      // include all requires and replaces, as they are dependencies
      for _, require := range parsedModFile.Require {
        dependencies[module.Path] = append(dependencies[module.Path], convertRelPath(repoPath, module.Path, require.Mod.Path))
      }

      for _, require := range parsedModFile.Replace {
        dependencies[module.Path] = append(dependencies[module.Path], convertRelPath(repoPath, module.Path, require.New.Path))
      }
    }

    if config.includePackages {
      goFiles := make(map[string][]string)

      pwd, err := os.Getwd()
      if err != nil {
      }

      extractGoFiles(pwd + module.Path[1:],  module.Path[2:], module.Path[2:], goFiles)

      fset := token.NewFileSet() // positions are relative to fset

      for keyQuestionMark, intPackage := range goFiles {
        var localPackageName string
        if (module.Path[1:] == keyQuestionMark) {
          localPackageName = keyQuestionMark
        } else {
          localPackageName = module.Path[1:] + keyQuestionMark
        }

        var fullPackageName string
        if strings.Contains(parsedModFile.Module.Mod.Path, keyQuestionMark) {
          fullPackageName = parsedModFile.Module.Mod.Path
        } else {
          fullPackageName = parsedModFile.Module.Mod.Path + keyQuestionMark
        }

        packagesPerModule[module.Path] = append(packagesPerModule[module.Path], localPackageName)
          for _, file := range intPackage {
            f, err := parser.ParseFile(fset, file, nil, parser.ImportsOnly)
            if err != nil {
            }

          for _, s := range f.Imports {
            packageNames.Insert(localPackageName, fullPackageName)
            modulePackageDependencies[fullPackageName] = append(modulePackageDependencies[fullPackageName], s.Path.Value)
          }
        }
      }
    }
	}

	return dependencies, dependencyNames, modulePackageDependencies, packageNames, packagesPerModule, nil
}

// isRelativeDep returns true if the dependency is relative to the module (starts with ./ or ../).
func isRelativeDep(path string) bool {
	return strings.HasPrefix(path, "./") || strings.HasPrefix(path, "../")
}

// convertRelPath converts a path relative to a module to a path relative to the repository root.
// it does nothing if the path does not start with ./ or ../.
func convertRelPath(repoPath string, modulePath, dependency string) string {
	if !isRelativeDep(dependency) {
		return dependency
	}

	// repo/./module => repo/module
	fullModulePath := filepath.Join(repoPath, modulePath)
	// repo/module/../dependency => repo/dependency
	fullDependencyPath := filepath.Join(fullModulePath, dependency)
	// repo/dependency => dependency
	trimmedPath := strings.TrimPrefix(fullDependencyPath, repoPath)
	if len(trimmedPath) == 0 {
		return "."
	}

	return fmt.Sprintf(".%s", trimmedPath)
}
