"""Smoke tests for the Streamlit application shell."""

from __future__ import annotations

import unittest

from streamlit.testing.v1 import AppTest


class AppSmokeTests(unittest.TestCase):
    def test_overview_renders_catalog_summary_without_exceptions(self) -> None:
        app = AppTest.from_file("app.py").run(timeout=30)

        self.assertEqual([], list(app.exception))
        markup = "\n".join(element.value for element in app.markdown)
        self.assertIn("<h1>Overview</h1>", markup)
        self.assertIn('class="lab-stat-label">Models</div>', markup)
        self.assertIn('class="lab-stat-value">5</div>', markup)
        self.assertIn('class="lab-stat-label">Test cases</div>', markup)
        self.assertIn('class="lab-stat-value">89</div>', markup)


if __name__ == "__main__":
    unittest.main()
