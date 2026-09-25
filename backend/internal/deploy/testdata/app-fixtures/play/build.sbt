name := """play-fixture"""
organization := "test.build-value"
version := "1.0"

lazy val root = (project in file(".")).enablePlugins(PlayScala)

scalaVersion := "3.3.8"

libraryDependencies += guice
