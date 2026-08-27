import unittest

from loadtest import LoadTestConfig, build_messages, compact_recorded_payload


class BuildMessagesTest(unittest.TestCase):
    def make_config(self, **overrides):
        values = {
            "url": "https://example.test/v1/chat/completions",
            "key": "test-key",
            "model": "test-model",
            "total_requests": 1,
            "duration_s": 1,
            "max_tokens": 32,
            "input_tokens": 10,
        }
        values.update(overrides)
        return LoadTestConfig(**values)

    def test_text_only_request_keeps_string_content(self):
        messages = build_messages(self.make_config())

        self.assertEqual(messages[0]["role"], "user")
        self.assertIsInstance(messages[0]["content"], str)

    def test_image_request_uses_its_own_prompt_and_object_data_url(self):
        messages = build_messages(self.make_config(
            image_data_url="data:image/png;base64,IMAGE",
            image_prompt="  inspect the image  ",
        ))

        content = messages[0]["content"]
        self.assertEqual([part["type"] for part in content], [
            "text", "text", "image_url",
        ])
        self.assertEqual(content[1]["text"], "inspect the image")
        self.assertEqual(
            content[2]["image_url"]["url"],
            "data:image/png;base64,IMAGE",
        )

    def test_image_and_video_keep_independent_prompts(self):
        messages = build_messages(self.make_config(
            image_data_url="data:image/png;base64,IMAGE",
            image_prompt="image task",
            video_data_url="data:video/mp4;base64,VIDEO",
            video_prompt="video task",
        ))

        content = messages[0]["content"]
        self.assertEqual([part["type"] for part in content], [
            "text", "text", "image_url", "text", "video_url",
        ])
        self.assertEqual(content[1]["text"], "image task")
        self.assertEqual(content[3]["text"], "video task")

    def test_recording_compacts_base64_without_changing_structure(self):
        payload = {
            "messages": [{
                "content": [{
                    "type": "video_url",
                    "video_url": {
                        "url": "data:video/mp4;base64,ABCDEFGHIJ",
                    },
                }],
            }],
        }

        compacted = compact_recorded_payload(payload)

        self.assertEqual(
            compacted["messages"][0]["content"][0]["video_url"]["url"],
            "data:video/mp4;base64,<10 base64 chars>",
        )
        self.assertEqual(
            payload["messages"][0]["content"][0]["video_url"]["url"],
            "data:video/mp4;base64,ABCDEFGHIJ",
        )


if __name__ == "__main__":
    unittest.main()
